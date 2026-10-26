package stored

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Validate implements viewPayload: it checks the frozen definition against
// its owning relation d in catalog v.
func (definition *View) Validate(v catalog.Read, d *catalog.Relation) error {
	if definition == nil || definition.syntax == nil || definition.query == nil || definition.query.Root == nil || definition.rule == 0 || len(definition.query.Root.Schema()) != len(d.Columns) {
		return fmt.Errorf("stuntgres: invalid view definition")
	}
	for _, usage := range definition.castUsages {
		if usage.Type == nil {
			return fmt.Errorf("stuntgres: missing view cast type")
		}
		if !ValidExpressionUsage(v, usage) {
			return fmt.Errorf("stuntgres: invalid view cast authority")
		}
		if err := ValidateUsageCollation(v, usage); err != nil {
			return err
		}
	}
	if err := definition.ValidateLiterals(v); err != nil {
		return err
	}
	if err := ValidateViewLeaves(v, definition); err != nil {
		return err
	}
	if err := definition.validateNamespaces(); err != nil {
		return err
	}
	for _, leaf := range definition.sources {
		source, ok := v.Relation(leaf.Ref.Address)
		if !ok || !ViewReferenceValid(v, d, leaf.Ref) || leaf.Ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: invalid view source identity")
		}
		namespace, ok := v.Namespace(source.Namespace())
		consumerNamespace, consumerOK := v.Namespace(d.Namespace())
		if !ok || namespace.Temporary != (ids.SessionToken{}) && (!consumerOK || consumerNamespace.Temporary != namespace.Temporary) {
			return fmt.Errorf("stuntgres: invalid view source lifetime")
		}
		if leaf.TableOIDRead && !source.HasSystemAttributes() {
			return fmt.Errorf("stuntgres: invalid view system attribute source")
		}
		for _, attribute := range leaf.Reads {
			frozen := false
			for _, id := range leaf.Attributes {
				frozen = frozen || id == attribute
			}
			if !frozen {
				return fmt.Errorf("stuntgres: invalid view read attribute")
			}
			if !source.HasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	for _, dependency := range definition.dependencies {
		ref := dependency.Ref
		address := ref.Address
		address.SubID = 0
		if !ViewReferenceValid(v, d, ref) {
			return fmt.Errorf("stuntgres: invalid view dependency identity")
		}
		object, ok := v.Find(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing view dependency")
		}
		if ref.Address.SubID != 0 {
			relation, ok := object.(*catalog.Relation)
			if !ok || !relation.HasSupportedAttribute(ref.Address.SubID) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	return nil
}

func ValidateViewLeaves(catalog catalog.Read, definition *View) error {
	return validateViewLeaves(catalog, definition, nil, nil)
}

// A routine action's associated presentations share one original source
// manifest. Coverage is local to this validation, never retained by a recipe.
type viewSourceCoverage struct {
	leaves []ViewLeaf
	seen   []bool
}

func sameViewLeaf(a, b ViewLeaf) bool {
	return a.Ref == b.Ref && a.Only == b.Only && a.AnalysisOnly == b.AnalysisOnly &&
		a.TableOIDRead == b.TableOIDRead && a.Mark == b.Mark &&
		slices.Equal(a.Attributes, b.Attributes) && slices.Equal(a.Reads, b.Reads)
}

func validateViewLeaves(catalog catalog.Read, definition *View, modifier func(*plan.CTEDefinition) error, coverage *viewSourceCoverage) error {
	if coverage != nil {
		if len(definition.sources) > len(coverage.leaves) {
			return fmt.Errorf("stuntgres: extra routine source manifest")
		}
		for occurrence, leaf := range definition.sources {
			if !sameViewLeaf(leaf, coverage.leaves[occurrence]) {
				return fmt.Errorf("stuntgres: inconsistent routine source manifest")
			}
		}
	}
	seen := make([]*plan.SourceNode, len(definition.sources))
	projects := make(map[uint32]*plan.ProjectNode, len(definition.frames))
	marked := make(map[uint32]bool)
	checkUsage := func(usage sqltype.Usage) error {
		if !usage.Valid() {
			return fmt.Errorf("stuntgres: invalid view query usage")
		}
		if err := ValidateUsageCollation(catalog, usage); err != nil {
			return err
		}
		if !ValidExpressionUsage(catalog, usage) {
			return fmt.Errorf("stuntgres: foreign view query type")
		}
		return nil
	}
	var visit func(plan.Node, uint32) error
	visit = func(node plan.Node, frame uint32) error {
		if node == nil {
			return fmt.Errorf("stuntgres: missing stored query node")
		}
		for _, column := range node.Schema() {
			if err := checkUsage(column.Usage); err != nil {
				return err
			}
		}
		if project, ok := node.(*plan.ProjectNode); ok && (project.Syntax != nil || definition.frames[project.Frame] == project) {
			frame = project.Frame
			frozen := definition.frames[frame]
			if frozen == nil || frozen.Syntax != project.Syntax {
				return fmt.Errorf("stuntgres: invalid stored query frame")
			}
			if prior := projects[frame]; prior != nil {
				if len(prior.Schema()) != len(project.Schema()) {
					return fmt.Errorf("stuntgres: inconsistent shared query width")
				}
				for i, column := range prior.Schema() {
					if column.Usage != project.Schema()[i].Usage {
						return fmt.Errorf("stuntgres: inconsistent shared query usage")
					}
				}
				return nil
			}
			projects[frame] = project
		}
		switch n := node.(type) {
		case *plan.ValuesNode:
			for _, row := range n.Rows {
				if len(row) != len(n.Schema()) {
					return fmt.Errorf("stuntgres: invalid view VALUES width")
				}
				for i, expr := range row {
					if expr == nil || expr.ResultUsage().Type != n.Schema()[i].Usage.Type || analyze.ExpressionUsesFrame(expr, frame) {
						return fmt.Errorf("stuntgres: invalid view VALUES cell")
					}
				}
			}
		case *plan.SetNode:
			if err := ValidateSetNode(n, nil, nil); err != nil {
				return err
			}
		case *plan.RecursiveUnionNode:
			if n.Definition == nil || !n.Definition.Recursive {
				return fmt.Errorf("stuntgres: invalid stored recursive definition")
			}
			if err := ValidateSetNode(&n.SetNode, n.Cycle, n.Search); err != nil {
				return err
			}
			if n.Search != nil || n.Cycle != nil {
				if n.Definition.UserWidth != len(n.LeftExpressions) {
					return fmt.Errorf("stuntgres: invalid stored original width")
				}
				scan, offset := analyze.RecursiveParentSlot(n.Right.Child, n.Definition, 0)
				if scan == nil {
					return fmt.Errorf("stuntgres: missing stored recursive parent")
				}
				for slot := n.Definition.UserWidth; slot < len(n.Schema()); slot++ {
					carry, ok := n.RightExpressions[slot].(plan.ColumnExpr)
					if !ok || carry.Slot != offset+slot || carry.Frame != n.Right.Frame || slot >= len(scan.Demand) || !scan.Demand[slot] {
						return fmt.Errorf("stuntgres: invalid stored recursive parent carry")
					}
				}
			}
		case *plan.LimitNode:
			for _, expr := range []expr.Expr{n.Offset, n.Count} {
				if expr != nil && (expr.ResultUsage().Type.ID() != sqltype.Int8 || analyze.ExpressionUsesFrame(expr, frame)) {
					return fmt.Errorf("stuntgres: invalid stored view bound")
				}
			}
			if n.WithTies {
				if n.Count == nil || len(n.PeerKeys) == 0 {
					return fmt.Errorf("stuntgres: invalid stored WITH TIES keys")
				}
				for _, key := range n.PeerKeys {
					if key.Expression == nil || key.Cache < -1 || key.Cache < 0 && (key.Slot < 0 || key.Slot >= len(n.Schema())) {
						return fmt.Errorf("stuntgres: invalid stored WITH TIES slot")
					}
				}
			}
		case *plan.FunctionFromNode:
			if err := exec.ValidateFunctionFromLayout(n); err != nil {
				return err
			}
			if err := validateTableOIDFunctionCaptures(n); err != nil {
				return err
			}
		case *plan.SetProjectNode:
			if n.Child == nil || n.Template == nil || n.Template.Group == nil || *n.Template.Group != n.Frame ||
				len(n.Calls) == 0 || n.BaseWidth < 0 || n.MarkerWidth != len(n.Template.Origins) ||
				len(n.Schema()) != n.BaseWidth+n.MarkerWidth+len(n.Template.Scalars) ||
				(len(n.Child.Schema()) != n.BaseWidth && len(n.Child.Schema()) != len(n.Schema())) ||
				len(n.CallSlots) != len(n.Calls) || len(n.Template.ScalarRequests) != len(n.Template.Scalars) ||
				n.ScalarSlots != nil && len(n.ScalarSlots) != len(n.Scalars) {
				return fmt.Errorf("stuntgres: invalid stored string set frame")
			}
			if err := exec.ValidateQuerySetSteps(n.Template.Steps, len(n.Template.Origins), len(n.Template.Scalars)); err != nil {
				return err
			}
			if err := exec.ValidateQuerySetSteps(n.Steps, len(n.Calls), len(n.Scalars)); err != nil {
				return err
			}
			if err := exec.ValidateQuerySetStageOrder(n.Template.Steps, n.Steps, n.CallSlots, n.ScalarSlots, len(n.Scalars)); err != nil {
				return err
			}
			base := n.BaseWidth
			for i, original := range n.Template.Origins {
				if !exec.SourceReturnsSet(original) || original.ResultUsage() != n.Schema()[base+i].Usage {
					return fmt.Errorf("stuntgres: invalid stored string marker layout")
				}
			}
			for i, scalar := range n.Template.Scalars {
				request := n.Template.ScalarRequests[i]
				if request < 0 || request >= len(n.Template.Expressions) || scalar == nil || exec.SourceReturnsSet(scalar) || n.Template.Expressions[request] == nil || exec.SourceReturnsSet(n.Template.Expressions[request]) || scalar.SourcePosition() != n.Template.Expressions[request].SourcePosition() || scalar.ResultUsage() != n.Template.Expressions[request].ResultUsage() {
					return fmt.Errorf("stuntgres: invalid stored string scalar owner")
				}
				if scalar == nil || scalar.ResultUsage() != n.Schema()[base+n.MarkerWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid stored string scalar layout")
				}
				for _, previous := range n.Template.ScalarRequests[:i] {
					if previous == request {
						return fmt.Errorf("stuntgres: duplicate stored string scalar owner")
					}
				}
			}
			for i, call := range n.Calls {
				slot := n.CallSlots[i]
				if slot < 0 || slot >= n.MarkerWidth {
					return fmt.Errorf("stuntgres: invalid stored string call slot")
				}
				for _, previous := range n.CallSlots[:i] {
					if previous == slot {
						return fmt.Errorf("stuntgres: duplicate stored string call slot")
					}
				}
				if !exec.SourceReturnsSet(call) || call.ResultUsage() != n.Schema()[base+slot].Usage {
					return fmt.Errorf("stuntgres: invalid stored string set usage")
				}
			}
			for i, scalar := range n.Scalars {
				slot := exec.QuerySetScalarSlot(n.ScalarSlots, i)
				if slot < 0 || slot >= len(n.Template.Scalars) || scalar == nil || exec.QuerySetDepth(scalar) != 0 || scalar.ResultUsage() != n.Schema()[base+n.MarkerWidth+slot].Usage || scalar.SourcePosition() != n.Template.Scalars[slot].SourcePosition() {
					return fmt.Errorf("stuntgres: invalid stored string scalar usage")
				}
				for previous := range i {
					if exec.QuerySetScalarSlot(n.ScalarSlots, previous) == slot {
						return fmt.Errorf("stuntgres: duplicate stored string scalar slot")
					}
				}
			}
		case *plan.SourceNode:
			if n.Occurrence < 0 || n.Occurrence >= len(seen) {
				return fmt.Errorf("stuntgres: invalid view source occurrence")
			}
			if prior := seen[n.Occurrence]; prior != nil && prior != n {
				return fmt.Errorf("stuntgres: duplicate view source occurrence")
			}
			seen[n.Occurrence] = n
			leaf := definition.sources[n.Occurrence]
			source, ok := catalog.Relation(leaf.Ref.Address)
			if !ok || n.Ref != leaf.Ref || len(n.Schema()) != len(leaf.Attributes) {
				return fmt.Errorf("stuntgres: invalid view source layout")
			}
			for _, attribute := range leaf.Reads {
				for slot, frozen := range leaf.Attributes {
					if frozen != attribute {
						continue
					}
					for current, live := range source.Attributes {
						if live == attribute && n.Schema()[slot].Usage != source.Columns[current].Usage {
							return fmt.Errorf("stuntgres: invalid view source usage")
						}
					}
				}
			}
		case *plan.JoinNode:
			if n.Kind > parser.JoinFull || n.LeftWidth != len(n.Left.Schema()) || n.RightWidth != len(n.Right.Schema()) || len(n.Schema()) != n.LeftWidth+n.RightWidth+len(n.Merges) {
				return fmt.Errorf("stuntgres: invalid view join frame")
			}
			for i, merge := range n.Merges {
				if merge == nil || merge.ResultUsage() != n.Schema()[n.LeftWidth+n.RightWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid view merged column usage")
				}
			}
		case *plan.RowLockNode:
			project := definition.frames[frame]
			if marked[frame] || project == nil || len(project.Syntax.Locking) == 0 || analyze.RowLockClause(n.FirstMode) != project.Syntax.Locking[0].Mode || len(n.Marks) > len(definition.sources) {
				return fmt.Errorf("stuntgres: invalid stored view rowmark")
			}
			for _, mark := range n.Marks {
				if mark.Mode != 0 && analyze.RowLockClause(mark.Mode) == "" || mark.Wait > parser.RowLockNowait {
					return fmt.Errorf("stuntgres: invalid stored view rowmark")
				}
			}
			marked[frame] = true
		case *plan.DerivedNode:
			if n.Query != nil {
				if n.Query.Root == nil || n.CTE != nil && n.CTE.Mutation != nil || len(n.Schema()) != len(n.Query.Root.Schema()) {
					return fmt.Errorf("stuntgres: invalid stored derived frame")
				}
			} else {
				mutation, err := checkedMutationDefinition(n.CTE)
				if err != nil || mutation.Returning == nil || n.Table == nil || n.Ordinal >= 0 || n.ScanFrame == 0 || len(n.Schema()) != len(n.CTE.OutputSchema()) {
					return fmt.Errorf("stuntgres: invalid stored mutation scan")
				}
				for i, column := range n.Schema() {
					if column.Usage != n.CTE.OutputSchema()[i].Usage {
						return fmt.Errorf("stuntgres: invalid stored mutation scan usage")
					}
				}
				if modifier != nil {
					if err := modifier(n.CTE); err != nil {
						return err
					}
				}
			}
		case *plan.WorktableScanNode:
			if n.Query != nil || n.CTE == nil || !n.CTE.Recursive || len(n.Schema()) != len(n.CTE.OutputSchema()) {
				return fmt.Errorf("stuntgres: invalid stored worktable identity")
			}
		case *plan.ProjectNode, *plan.EvaluationNode, *plan.FilterNode, *plan.SortNode, *plan.AggregateNode, *plan.WindowNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SingletonNode:
		default:
			return fmt.Errorf("stuntgres: invalid view query node %T", node)
		}
		var expressionError error
		validateExpression := func(bound expr.Expr, contextual bool) {
			if expressionError != nil {
				return
			}
			plan.WalkExpression(bound, func(expr expr.Expr) {
				if expressionError != nil {
					return
				}
				if unresolved, ok := expr.(plan.UnknownOutput); ok {
					if !contextual {
						expressionError = fmt.Errorf("stuntgres: invalid view query usage")
						return
					}
					// Set operands retain their pre-common-type literal analysis,
					// not an executable value with an invented usage. Only these
					// explicit contextual occurrences may lack a descriptor.
					literal, ok := unresolved.Raw.(parser.LiteralExpr)
					if !ok || unresolved.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
						expressionError = fmt.Errorf("stuntgres: invalid contextual view output")
					}
					return
				}
				if expressionError = checkUsage(expr.ResultUsage()); expressionError != nil {
					return
				}
				if system, ok := expr.(plan.TableOIDExpr); ok && (system.Scope != 0 || system.Frame != 0 || system.QueryOccurrence != 0) {
					_, expressionError = definition.tableOIDSource(catalog, analyze.ViewColumn{TableOID: true, Owner: system.Owner, Scope: system.Scope, Frame: system.Frame, Occurrence: system.QueryOccurrence})
					if expressionError != nil {
						return
					}
				}
				if child, ok := expr.(plan.SubqueryExpr); ok {
					if child.QueryPlan == nil || child.QueryPlan.Root == nil {
						expressionError = fmt.Errorf("stuntgres: invalid stored subquery")
						return
					}
					if child.Kind != parser.SubqueryScalar && child.Kind != parser.SubqueryExists && len(child.Comparisons) != len(child.QueryPlan.Root.Schema()) {
						expressionError = fmt.Errorf("stuntgres: invalid stored comparison width")
						return
					}
					expressionError = visit(child.QueryPlan.Root, frame)
				}
			})
		}
		if project, ok := node.(*plan.ProjectNode); ok {
			for _, expr := range project.Expressions {
				validateExpression(expr, false)
			}
			for _, expr := range project.AnalysisExpressions {
				validateExpression(expr, true)
			}
		} else if stage, ok := node.(*plan.SetProjectNode); ok {
			for _, call := range stage.Calls {
				validateExpression(call, false)
			}
			for _, scalar := range stage.Scalars {
				validateExpression(scalar, false)
			}
			if stage.Template != nil {
				for _, root := range stage.Template.Expressions {
					_, contextual := root.(plan.UnknownOutput)
					validateExpression(root, contextual)
				}
				for _, origin := range stage.Template.Origins {
					validateExpression(origin, false)
				}
				for _, scalar := range stage.Template.Scalars {
					validateExpression(scalar, false)
				}
			}
		} else {
			plan.QueryExpressions(node, func(expr expr.Expr) { validateExpression(expr, false) })
		}
		if expressionError != nil {
			return expressionError
		}
		left, right := plan.QueryInputs(node)
		if left != nil {
			if err := visit(left, frame); err != nil {
				return err
			}
		}
		if right != nil {
			if err := visit(right, frame); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(definition.query.Root, 0); err != nil {
		return err
	}
	for occurrence, source := range seen {
		if source == nil {
			if coverage == nil {
				return fmt.Errorf("stuntgres: invalid view source manifest")
			}
		} else if coverage != nil {
			coverage.seen[occurrence] = true
		}
	}
	if len(projects) != len(definition.frames) {
		return fmt.Errorf("stuntgres: invalid view query frame manifest")
	}
	for frame, project := range projects {
		if project.Syntax != nil && project.Syntax.Locking != nil && !marked[frame] {
			return fmt.Errorf("stuntgres: missing stored view rowmark")
		}
	}
	return nil
}

func ValidateSetNode(n *plan.SetNode, cycle *plan.CycleRecipe, search *plan.SearchRecipe) error {
	width := len(n.Schema())
	leftWidth, groupingWidth := width, width
	if cycle != nil {
		if cycle.MarkSlot < 0 || width != cycle.MarkSlot+2 ||
			n.Schema()[cycle.MarkSlot].Usage != cycle.Mark.ResultUsage() ||
			n.Schema()[cycle.MarkSlot+1].Usage.Type != cycle.Path {
			return fmt.Errorf("stuntgres: invalid stored cycle layout")
		}
		leftWidth = cycle.MarkSlot
		groupingWidth--
	}
	if search != nil {
		if search.Syntax == nil || search.Result == nil {
			return fmt.Errorf("stuntgres: invalid stored search recipe")
		}
		expected := search.Slot + 1
		if cycle != nil {
			expected += 2
		}
		if search.Slot < 0 || width != expected || cycle != nil && cycle.MarkSlot != search.Slot+1 ||
			n.Schema()[search.Slot].Usage != search.Result.Usage() ||
			search.Record == nil || !search.Record.IsComposite() || search.Record.ID() != 2249 ||
			(search.Syntax.Breadth && search.Result != search.Record) ||
			(!search.Syntax.Breadth && (!sqltype.OccurrenceRecordArray(search.Result) || search.Result.Element() != search.Record)) {
			return fmt.Errorf("stuntgres: invalid stored search layout")
		}
		if !n.All && len(search.Grouping) != search.Record.CompositeLen() || n.All && len(search.Grouping) != 0 {
			return fmt.Errorf("stuntgres: invalid stored search equality")
		}
		offset := 0
		if search.Syntax.Breadth {
			offset = 1
		}
		if len(search.Keys)+offset != search.Record.CompositeLen() {
			return fmt.Errorf("stuntgres: invalid stored search keys")
		}
		for i, key := range search.Keys {
			if key < 0 || key >= search.Slot || n.Schema()[key].Usage != search.Record.CompositeMember(i+offset).Usage {
				return fmt.Errorf("stuntgres: invalid stored search key usage")
			}
		}
		leftWidth = search.Slot
		groupingWidth--
	}
	if n.Left == nil || n.Right == nil || len(n.Left.Schema()) != leftWidth || len(n.Right.Schema()) != width || len(n.LeftExpressions) != leftWidth || len(n.RightExpressions) != width {
		return fmt.Errorf("stuntgres: invalid stored set width")
	}
	if n.Operation != "union" && n.Operation != "intersect" && n.Operation != "except" {
		return fmt.Errorf("stuntgres: invalid stored set operation")
	}
	grouped := !(n.Operation == "union" && n.All)
	if grouped && len(n.Grouping) != groupingWidth || !grouped && len(n.Grouping) != 0 {
		return fmt.Errorf("stuntgres: invalid stored set grouping")
	}
	for i, column := range n.Schema() {
		if n.RightExpressions[i] == nil || n.RightExpressions[i].ResultUsage() != column.Usage ||
			i < leftWidth && (n.LeftExpressions[i] == nil || n.LeftExpressions[i].ResultUsage() != column.Usage) {
			return fmt.Errorf("stuntgres: invalid stored set coercion")
		}
	}
	return nil
}
