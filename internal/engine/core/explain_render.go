package core

import (
	"fmt"
	"maps"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

type explainTree struct {
	label       string
	details     []string
	children    []*explainTree
	estimate    plan.ExplainEstimate
	restriction expr.Expr
}

type explainSubplan struct {
	query        *plan.Query
	label        string
	frames       map[uint32]plan.Node
	occurrences  map[int]*plan.SourceNode
	auth         AuthorizationView
	leaves       []exec.Leaf
	image        string
	policyTarget ids.ObjectAddress
}

type explainRenderer struct {
	scope                        *StatementScope
	auth                         AuthorizationView
	options                      explainOptions
	path                         []ids.ObjectAddress
	frames                       map[uint32]plan.Node
	occurrences                  map[int]*plan.SourceNode
	ctes                         map[*plan.CTEDefinition]*explainTree
	worktables                   map[*plan.CTEDefinition]float64
	subplans                     []explainSubplan
	producers                    []*explainTree
	viewActive                   map[ids.ObjectAddress]bool
	policyTarget                 ids.ObjectAddress
	policyCommand                catalog.PolicyCommand
	leaves                       []exec.Leaf
	targetOnly                   bool
	rewrites                     view.RuleCTECommands
	assignmentRoots              []expr.Expr
	policyReads, policyReturning bool
	policyPrincipal              authorization.PrincipalRef
	image                        string
	scanFilter                   *plan.FilterNode
	scanDecision                 plan.ScanDecision
	frame                        uint32
}

func newExplainRenderer(scope *StatementScope, auth AuthorizationView, options explainOptions) (*explainRenderer, error) {
	path, err := ReferencePath(auth)
	if err != nil {
		return nil, err
	}
	return &explainRenderer{scope: scope, auth: auth, options: options, path: path, frame: 1, frames: make(map[uint32]plan.Node), occurrences: make(map[int]*plan.SourceNode), ctes: make(map[*plan.CTEDefinition]*explainTree), viewActive: make(map[ids.ObjectAddress]bool)}, nil
}

func (r *explainRenderer) objectName(namespace ids.ObjectAddress, name string) string {
	text := catalog.Identifier(name)
	if r.options.verbose {
		if ns, ok := r.auth.Catalog.Namespace(namespace); ok {
			text = catalog.Identifier(ns.Name()) + "." + text
		}
	}
	return text
}

func (r *explainRenderer) register(root plan.Node) {
	if root == nil {
		return
	}
	switch node := root.(type) {
	case *plan.SourceNode:
		r.occurrences[node.Occurrence] = node
	case *plan.WorktableScanNode, *plan.DerivedNode:
		return
	}
	left, right := plan.QueryInputs(root)
	r.register(left)
	r.register(right)
	if project, ok := root.(*plan.ProjectNode); ok {
		r.frames[project.Frame] = project.Child
	}
}

func (r *explainRenderer) subplan(query *plan.Query) string {
	for _, subplan := range r.subplans {
		if subplan.query == query && subplan.image == r.image && subplan.policyTarget == r.policyTarget {
			return subplan.label
		}
	}
	label := fmt.Sprintf("SubPlan %d", len(r.subplans)+1)
	r.subplans = append(r.subplans, explainSubplan{query: query, label: label, frames: maps.Clone(r.frames), occurrences: maps.Clone(r.occurrences), auth: r.auth, leaves: r.leaves, image: r.image, policyTarget: r.policyTarget})
	return label
}

func (r *explainRenderer) query(query *plan.Query) (*explainTree, error) {
	if query == nil || query.Root == nil {
		return nil, fmt.Errorf("stuntgres: missing logical EXPLAIN query")
	}
	previousFilter, previousDecision := r.scanFilter, r.scanDecision
	defer func() { r.scanFilter, r.scanDecision = previousFilter, previousDecision }()
	r.scanFilter = exec.HashQueryFilter(query.Root)
	if r.scanFilter != nil {
		frame := query.Root.(*plan.ProjectNode).Frame
		if frame == 0 {
			frame = 1
		}
		r.scanDecision = r.explainScan(r.scanFilter, frame)
	}
	r.register(query.Root)
	return r.node(query.Root)
}

func (r *explainRenderer) node(root plan.Node) (*explainTree, error) {
	if err := r.scope.executionContext().Err(); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("stuntgres: missing logical EXPLAIN child")
	}
	if project, ok := root.(*plan.ProjectNode); ok {
		saved := r.frame
		r.frame = project.Frame
		if r.frame == 0 {
			r.frame = 1
		}
		defer func() { r.frame = saved }()
	}
	left, right := plan.QueryInputs(root)
	if derived, ok := root.(*plan.DerivedNode); ok && derived.CTE != nil {
		left, right = nil, nil
	}
	if project, ok := root.(*plan.ProjectNode); ok && r.frames[project.Frame] == nil {
		r.frames[project.Frame] = project.Child
	}
	if plan.ExplainInactive(root) {
		return r.node(left)
	}
	tree := &explainTree{}
	var childEstimates [2]plan.ExplainEstimate
	estimates := childEstimates[:0]
	recursive, isRecursive := root.(*plan.RecursiveUnionNode)
	if isRecursive {
		if r.worktables == nil {
			r.worktables = make(map[*plan.CTEDefinition]float64)
		}
		previous, existed := r.worktables[recursive.Definition]
		defer func() {
			if existed {
				r.worktables[recursive.Definition] = previous
			} else {
				delete(r.worktables, recursive.Definition)
			}
		}()
	}
	for _, child := range []plan.Node{left, right} {
		if child == nil {
			continue
		}
		childTree, err := r.node(child)
		if err != nil {
			return nil, err
		}
		tree.children = append(tree.children, childTree)
		estimates = append(estimates, childTree.estimate)
		if isRecursive && child == left {
			r.worktables[recursive.Definition] = min(plan.ExplainEstimateLimit, max(0, childTree.estimate.Rows*r.scope.Op.RecursiveWorktableFactor))
		}
	}
	sourceRows := float64(1000)
	detail := func(label string, expression expr.Expr, context plan.Node) error {
		if expression == nil {
			return nil
		}
		text, err := r.expression(context, expression)
		if err == nil {
			tree.details = append(tree.details, label+": "+text)
		}
		return err
	}
	list := func(label string, expressions []expr.Expr, context plan.Node) error {
		if len(expressions) == 0 {
			return nil
		}
		text, err := r.expressions(context, expressions)
		if err == nil {
			tree.details = append(tree.details, label+": "+text)
		}
		return err
	}
	switch n := root.(type) {
	case *plan.SourceNode:
		r.occurrences[n.Occurrence] = n
		relation, ok := r.auth.Catalog.Relation(n.Ref.Address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		tree.label = "Logical Scan on " + r.objectName(relation.Namespace(), relation.Name())
		if n.RangeName != "" && n.RangeName != relation.Name() {
			tree.label += " AS " + catalog.Identifier(n.RangeName)
		}
		var err error
		sourceRows, err = r.sourceStatistics(tree, n, relation)
		if err != nil {
			return nil, err
		}
		if err := r.sourceDetails(tree, n, relation); err != nil {
			return nil, err
		}
		for _, child := range tree.children {
			estimates = append(estimates, child.estimate)
			sourceRows = child.estimate.Rows
		}
	case *plan.SingletonNode:
		tree.label = "Singleton"
	case *plan.ValuesNode:
		tree.label = "Values"
		if r.options.verbose {
			for _, row := range n.Rows {
				text, err := r.valueRow(root, row)
				if err != nil {
					return nil, err
				}
				tree.details = append(tree.details, "Output: "+text)
			}
		}
	case *plan.FilterNode:
		tree.label = "Filter"
		if err := detail("Filter", n.Predicate, n.Child); err != nil {
			return nil, err
		}
	case *plan.SortNode:
		tree.label = "Sort"
		keys, err := r.sortKeys(n.Child, n.Keys)
		if err != nil {
			return nil, err
		}
		tree.details = append(tree.details, "Sort Key: "+keys)
	case *plan.ProjectNode:
		tree.label = "Project"
		if r.options.verbose {
			if err := list("Output", n.Expressions, n.Child); err != nil {
				return nil, err
			}
		}
	case *plan.EvaluationNode:
		tree.label = "Evaluate"
		if err := list("Expressions", n.Expressions, n.Child); err != nil {
			return nil, err
		}
	case *plan.AggregateNode:
		tree.label = "Aggregate"
		if len(n.Keys) > 0 {
			method := "hash grouping"
			if r.scope.PlannerMethods.NoHashAgg {
				method = "equality grouping"
			}
			tree.details = append(tree.details, "Method: "+method)
		}
		if err := list("Group Key", n.Keys, n.Child); err != nil {
			return nil, err
		}
		if n.Sets != nil {
			sets := make([]string, len(n.Sets))
			for i, set := range n.Sets {
				keys := make([]expr.Expr, len(set))
				for j, key := range set {
					keys[j] = n.Keys[key]
				}
				text, err := r.expressions(n.Child, keys)
				if err != nil {
					return nil, err
				}
				sets[i] = "(" + text + ")"
			}
			tree.details = append(tree.details, "Grouping Sets: "+strings.Join(sets, ", "))
		}
		for _, call := range n.Calls {
			text, err := r.aggregateCall(n.Child, call)
			if err != nil {
				return nil, err
			}
			tree.details = append(tree.details, "Aggregate: "+text)
		}
	case *plan.DistinctNode:
		tree.label = "Distinct"
		method := "hash deduplication"
		if r.scope.PlannerMethods.NoHashAgg {
			method = "equality deduplication"
		}
		tree.details = append(tree.details, "Method: "+method)
		if err := list("Distinct Key", n.Expressions, n.Child); err != nil {
			return nil, err
		}
	case *plan.DistinctOnNode:
		tree.label = "Distinct On"
		if err := list("Distinct On Key", n.Expressions, n.Child); err != nil {
			return nil, err
		}
	case *plan.JoinNode:
		kind := "Inner"
		switch n.Kind {
		case parser.JoinLeft:
			kind = "Left"
		case parser.JoinRight:
			kind = "Right"
		case parser.JoinFull:
			kind = "Full"
		default:
			if n.Predicate == nil {
				kind = "Cross"
			}
		}
		tree.label = kind + " Join"
		method := "nested-loop"
		if !r.scope.PlannerMethods.NoHashJoin && exec.HashJoinEligible(n, r.frame) {
			method = "hash-assisted"
		}
		tree.details = append(tree.details, "Method: "+method)
		if n.CorrelatedRight {
			tree.details = append(tree.details, "Lateral: correlated right input")
		}
		if err := detail("Join Condition", n.Predicate, root); err != nil {
			return nil, err
		}
	case *plan.DerivedNode:
		tree.label = "Derived Scan"
		if n.CTE != nil {
			tree.label = "CTE Scan on " + catalog.Identifier(n.CTE.Name)
			producer, err := r.cte(n.CTE)
			if err != nil {
				return nil, err
			}
			// Producer work contributes once at command level, not once per
			// reference. Each scan still has its own linear local work.
			estimates = append(estimates[:0], plan.ExplainEstimate{Rows: producer.estimate.Rows})
		}
		if alias := explainDerivedAlias(n); alias != "" {
			tree.details = append(tree.details, "Alias: "+catalog.Identifier(alias))
		}
	case *plan.WorktableScanNode:
		tree.label = "Worktable Scan on " + catalog.Identifier(explainDerivedAlias(&n.DerivedNode))
		if rows, ok := r.worktables[n.CTE]; ok {
			sourceRows = rows
		}
	case *plan.SetNode:
		tree.label = strings.ToUpper(n.Operation)
		if n.All {
			tree.label += " ALL"
		}
	case *plan.RecursiveUnionNode:
		tree.label = "Recursive Union"
		if n.All {
			tree.label += " ALL"
		}
		if n.Search != nil {
			if n.Search.Syntax == nil {
				return nil, fmt.Errorf("stuntgres: missing EXPLAIN search recipe")
			}
			direction := "DEPTH FIRST"
			if n.Search.Syntax.Breadth {
				direction = "BREADTH FIRST"
			}
			tree.details = append(tree.details, "SEARCH: "+direction+" BY "+explainIdentifiers(n.Search.Syntax.Keys)+" SET "+catalog.Identifier(n.Search.Syntax.Sequence.Text))
		}
		if n.Cycle != nil {
			if n.Cycle.Syntax == nil {
				return nil, fmt.Errorf("stuntgres: missing EXPLAIN cycle recipe")
			}
			mark, err := r.expression(root, n.Cycle.Mark)
			if err != nil {
				return nil, err
			}
			fallback, err := r.expression(root, n.Cycle.Fallback)
			if err != nil {
				return nil, err
			}
			tree.details = append(tree.details, "CYCLE: "+explainIdentifiers(n.Cycle.Syntax.Keys)+" SET "+catalog.Identifier(n.Cycle.Syntax.Mark.Text)+" TO "+mark+" DEFAULT "+fallback+" USING "+catalog.Identifier(n.Cycle.Syntax.Path.Text))
		}
	case *plan.LimitNode:
		tree.label = "Limit"
		if n.WithTies {
			tree.label += " WITH TIES"
		}
		if err := detail("Offset", n.Offset, n.Child); err != nil {
			return nil, err
		}
		if err := detail("Count", n.Count, n.Child); err != nil {
			return nil, err
		}
	case *plan.RowLockNode:
		tree.label = "Row Lock"
		for i, mark := range n.Marks {
			if mark.Mode == 0 {
				continue
			}
			mode := map[lock.Mode]string{lock.ForUpdate: "FOR UPDATE", lock.ForNoKeyUpdate: "FOR NO KEY UPDATE", lock.ForShare: "FOR SHARE", lock.ForKeyShare: "FOR KEY SHARE"}[mark.Mode]
			wait := "WAIT"
			if mark.Wait == parser.RowLockNowait {
				wait = "NOWAIT"
			} else if mark.Wait == parser.RowLockSkip {
				wait = "SKIP LOCKED"
			}
			target := fmt.Sprintf("source %d", i+1)
			if source := r.occurrences[i]; source != nil {
				target = catalog.Identifier(source.RangeName)
			}
			tree.details = append(tree.details, mode+" OF "+target+" "+wait)
		}
	case *plan.WindowNode:
		tree.label = "Window"
		for i, call := range n.Calls {
			if call.AnalysisOnly {
				continue
			}
			text, err := r.windowCall(n, i)
			if err != nil {
				return nil, err
			}
			tree.details = append(tree.details, "Window: "+text)
		}
	case *plan.FunctionFromNode:
		tree.label = "Function Scan"
		if n.Ordinality {
			tree.details = append(tree.details, "Ordinality: true")
		}
		if n.Correlated {
			tree.details = append(tree.details, "Lateral: correlated function input")
		}
		context := root
		if n.Correlated && r.frames[n.Frame] != nil {
			context = r.frames[n.Frame]
		}
		if err := list("Functions", n.Calls, context); err != nil {
			return nil, err
		}
	case *plan.SetProjectNode:
		tree.label = "Set Project"
		if err := list("Functions", n.Calls, n.Child); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid logical EXPLAIN node")
	}
	r.discoverSubplans(root)
	if r.options.verbose {
		switch root.(type) {
		case *plan.ProjectNode, *plan.ValuesNode, *plan.SourceNode:
		default:
			outputs := make([]string, 0, len(root.Schema()))
			for i := range root.Schema() {
				if explainPrivateSlot(root, i) {
					continue
				}
				text, ok, err := r.slot(root, i)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing EXPLAIN output slot")
				}
				outputs = append(outputs, text)
			}
			tree.details = append(tree.details, "Output: "+strings.Join(outputs, ", "))
		}
	}
	estimate, err := plan.EstimateExplainNode(root, estimates, sourceRows)
	if err != nil {
		return nil, err
	}
	if filter, ok := root.(*plan.FilterNode); ok && len(estimates) != 0 {
		estimate, err = r.filterEstimate(filter.Child, filter.Predicate, estimate, estimates[0].Rows)
		if err != nil {
			return nil, err
		}
	}
	if tree.restriction != nil {
		inputRows := estimate.Rows
		estimate, err = plan.EstimateExplainNode(&plan.FilterNode{Predicate: tree.restriction}, []plan.ExplainEstimate{estimate}, 0)
		if err != nil {
			return nil, err
		}
		estimate, err = r.filterEstimate(root, tree.restriction, estimate, inputRows)
		if err != nil {
			return nil, err
		}
	}
	if root == r.scanFilter {
		if r.scanDecision.Method == plan.HashCandidates {
			tree.details = append(tree.details, "Access Method: Hash Candidates", "Index: "+r.scanDecision.IndexName)
		} else {
			tree.details = append(tree.details, "Access Method: Full Scan")
		}
		if r.options.costs {
			tree.details = append(tree.details, fmt.Sprintf("CPU Cost: %.6g", r.scanDecision.Cost()))
		}
	}
	tree.estimate = estimate
	return tree, nil
}

func explainDerivedAlias(node *plan.DerivedNode) string {
	if node.RangeName != "" {
		return node.RangeName
	}
	if node.CTE != nil {
		return node.CTE.Name
	}
	if node.Table != nil && len(node.Table.Name.Parts) != 0 {
		return node.Table.Name.Parts[len(node.Table.Name.Parts)-1].Text
	}
	return ""
}

func (r *explainRenderer) lines(tree *explainTree, depth int, lines *[]string) {
	indent := strings.Repeat("  ", depth)
	line := indent + tree.label
	if r.options.costs {
		line += fmt.Sprintf(" (estimated rows=%.0f logical cost=%.2f)", tree.estimate.RoundedRows(), tree.estimate.Cost)
	}
	*lines = append(*lines, line)
	for _, detail := range tree.details {
		*lines = append(*lines, indent+"  "+detail)
	}
	for _, child := range tree.children {
		r.lines(child, depth+1, lines)
	}
}

func (r *explainRenderer) discoverSubplans(root plan.Node) {
	visit := func(expression expr.Expr) {
		plan.WalkDemandedExpression(expression, func(node expr.Expr) {
			if subquery, ok := node.(plan.SubqueryExpr); ok && !subquery.CTEEdge && subquery.QueryPlan != nil {
				r.subplan(subquery.QueryPlan)
			}
		})
	}
	switch node := root.(type) {
	case *plan.ProjectNode:
		for _, expression := range node.Expressions {
			visit(expression)
		}
	case *plan.SetProjectNode:
		for _, call := range node.Calls {
			visit(call)
		}
		for _, scalar := range node.Scalars {
			visit(scalar)
		}
	default:
		plan.QueryExpressions(root, visit)
	}
}

func explainIdentifiers(identifiers []parser.Identifier) string {
	parts := make([]string, len(identifiers))
	for i, identifier := range identifiers {
		parts[i] = catalog.Identifier(identifier.Text)
	}
	return strings.Join(parts, ", ")
}
