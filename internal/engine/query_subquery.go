package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSubquery(raw parser.SubqueryExpr) (expr.Expr, error) {
	if b.consumer.defaultConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in DEFAULT expression", raw.Pos)
	}
	if b.consumer.checkConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in check constraint", raw.Pos)
	}
	if b.consumer.indexPredicateConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", raw.Pos)
	}
	if b.consumer.indexConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", raw.Pos)
	}
	if b.target.generatedOwner != nil {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in column generation expression", raw.Pos)
	}
	b.query.subqueryDemand = true
	var left []comparisonOperand
	var leftExpansions map[int]plan.CompositeExpansionBinding
	var leftConstructor expr.Expr
	var erasedLeftType sqltype.ID
	if raw.Left != nil {
		var err error
		if row, ok := raw.Left.(parser.RowExpr); ok {
			left, leftExpansions, err = b.rowComparisonOperands(row)
		} else {
			var operand comparisonOperand
			operand, err = b.comparisonOperand(raw.Left)
			if constructor, ok := operand.node.(plan.RowExpr); ok && constructor.Raw != nil && !constructor.Usage.Type.IsDomain() {
				// A cast to a named composite can still analyze to a ROW
				// constructor. Quantified subqueries compare its fields;
				// a domain coercion or scalar subquery remains one value.
				left = make([]comparisonOperand, len(constructor.Fields))
				for i, field := range constructor.Fields {
					left[i] = comparisonOperand{node: field}
				}
				leftExpansions = constructor.Expansions
				if b.params.noParameters || b.consumer.materializedDefinition {
					erasedLeftType = constructor.Usage.Type.ID()
					leftConstructor, err = b.normalizeQuantifiedConstructor(raw.Left, constructor)
					if err != nil {
						return nil, err
					}
				}
			} else {
				left = []comparisonOperand{operand}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	query, err := b.bindSelect(raw.Query, false)
	if err != nil {
		return nil, err
	}
	project := query.Root.(*plan.ProjectNode)
	width := len(project.Schema())
	usage := b.typ(sqltype.Bool).Usage()
	if raw.Kind == parser.SubqueryScalar {
		if width != 1 {
			return nil, expr.ErrorAt("42601", "subquery must return only one column", raw.Pos)
		}
		usage = project.Schema()[0].Usage
	} else if raw.Kind != parser.SubqueryExists {
		if width < len(left) {
			return nil, expr.ErrorAt("42601", "subquery has too few columns", raw.Pos)
		}
		if width > len(left) {
			return nil, expr.ErrorAt("42601", "subquery has too many columns", raw.Pos)
		}
		if width == 0 {
			return nil, expr.ErrorAt("0A000", "cannot compare rows of zero length", raw.Pos)
		}
	}
	presenceOnly := raw.Kind == parser.SubqueryExists && canSimplifyExists(query)
	if presenceOnly {
		query, err = presenceQueryPlan(query)
		if err != nil {
			return nil, err
		}
		project = query.Root.(*plan.ProjectNode)
	}
	properties := queryDemandProperties(project, presenceOnly)
	properties.StageDependent = true
	e := plan.SubqueryExpr{Info: b.info(usage, raw.Pos, properties), Kind: raw.Kind, QueryPlan: query, Negate: raw.Negate, PresenceOnly: presenceOnly}
	e.LeftExpansions = leftExpansions
	e.LeftConstructor, e.ErasedLeftType = leftConstructor, erasedLeftType
	if raw.Kind == parser.SubqueryScalar || raw.Kind == parser.SubqueryExists {
		return e, nil
	}
	e.Operator = raw.Operator.Parts[len(raw.Operator.Parts)-1].Text
	operator := sqltype.CompareEqual
	switch e.Operator {
	case "=":
	case "<>":
		operator = sqltype.CompareNotEqual
	case "<":
		operator = sqltype.CompareLess
	case "<=":
		operator = sqltype.CompareLessEqual
	case ">":
		operator = sqltype.CompareGreater
	case ">=":
		operator = sqltype.CompareGreaterEqual
	default:
		return nil, fmt.Errorf("stuntgres: invalid subquery comparison operator")
	}
	e.Comparisons = make([]plan.ComparisonExpr, width)
	if width > 1 && operator > sqltype.CompareNotEqual {
		e.Ordering = make([]sqltype.Comparison, width)
	}
	for i, column := range project.Schema() {
		right := plan.ColumnExpr{Info: b.info(column.Usage, raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: project.Frame, Slot: i}
		bound, err := b.bindComparisonOperands(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, operator, left[i], comparisonOperand{node: right})
		if err != nil {
			return nil, err
		}
		e.Comparisons[i] = bound.(plan.ComparisonExpr)
		e.Property = expr.MergeProperties(e.Property, e.Comparisons[i].Left.Properties())
		if len(e.Ordering) != 0 {
			ref, conflict := e.Comparisons[i].Predicate.InputCollation()
			e.Ordering[i], err = sqltype.BindComparison(e.Comparisons[i].Left.ResultUsage().Type, e.Comparisons[i].Right.ResultUsage().Type, ref, conflict)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := b.checkRowComparisonInterpretation(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, e.Comparisons); err != nil {
		return nil, err
	}
	if raw.Kind == parser.SubqueryAny && operator == sqltype.CompareEqual && !properties.RowDependent {
		families := make([]sqltype.Grouping, width)
		for i, comparison := range e.Comparisons {
			families[i], err = comparison.Predicate.EqualityGrouping()
			if err != nil {
				families = nil
				break
			}
		}
		e.Membership = families
	}
	return e, nil
}

func (c *EvalContext) EvalSubquery(e plan.SubqueryExpr) (value sqltype.Value, err error) {
	if err = evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.query == nil || c.query.source == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing subquery statement capture")
	}
	if e.UpdateWidth != 0 {
		return c.subqueryUpdateOutputs(e)
	}
	if len(e.Membership) != 0 {
		return c.subqueryHashMembership(e)
	}
	var smallLeft [4]sqltype.Value
	left := smallLeft[:min(len(e.Comparisons), len(smallLeft))]
	if len(e.Comparisons) > len(smallLeft) {
		left = make([]sqltype.Value, len(e.Comparisons))
	}
	for i, comparison := range e.Comparisons {
		left[i], err = comparison.Left.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() {
		closeErr := stage.Close()
		if err == nil {
			err = closeErr
		}
	}()
	if e.JSONArray != nil {
		return c.subqueryJSONArray(e, &state, &stage)
	}
	if e.Kind == parser.SubqueryExists && e.PresenceOnly {
		_, present, failure := stage.nextOccurrence(c.Context)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		return sqltype.Boolean(e.Usage.Type, present), nil
	}
	if e.Kind == parser.SubqueryExists {
		present, failure := stage.nextProjected(c.Context, nil)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		return sqltype.Boolean(e.Usage.Type, present), nil
	}
	width := len(stage.Schema())
	var small [4]sqltype.Value
	row := small[:min(width, len(small))]
	if width > len(small) {
		row = make([]sqltype.Value, width)
	}
	if e.Kind == parser.SubqueryScalar {
		present, failure := stage.Next(c.Context, row)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if !present {
			return sqltype.ResultNull(e.Usage), nil
		}
		value = row[0]
		extra, failure := stage.nextProjected(c.Context, nil)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if extra {
			return sqltype.Value{}, sqlError("21000", "more than one row returned by a subquery used as an expression")
		}
		return value, nil
	}
	unknown := false
	for {
		present, failure := stage.Next(c.Context, row)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if !present {
			if e.Kind == parser.SubqueryRow || unknown {
				return sqltype.Null(e.Usage.Type), nil
			}
			truth := e.Kind == parser.SubqueryAll
			if e.Negate {
				truth = !truth
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
		rightContext := state.evaluation(c.Context)
		rightContext.Row = row
		answer, failure := e.Compare(&rightContext, left)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if e.Kind == parser.SubqueryRow {
			extra, failure := stage.nextProjected(c.Context, nil)
			if failure != nil {
				return sqltype.Value{}, failure
			}
			if extra {
				return sqltype.Value{}, sqlError("21000", "more than one row returned by a subquery used as an expression")
			}
			return answer, nil
		}
		if answer.IsNull() {
			unknown = true
			continue
		}
		truth, failure := answer.Boolean()
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if e.Kind == parser.SubqueryAll && !truth || e.Kind == parser.SubqueryAny && truth {
			if e.Negate {
				truth = !truth
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
	}
}

// Full analysis precedes this demand decision. A represented OFFSET, actual local
// aggregate, HAVING or rowmark retains ordinary tuple projection.
// Only a proven positive/NULL literal LIMIT is removable; parameters are not
// evaluated during binding. Retain all other plans conservatively.
func canSimplifyExists(query *plan.Query) bool {
	project := query.Root.(*plan.ProjectNode)
	s := project.Syntax
	if s != nil && (s.Having != nil || s.Locking != nil || s.Bounds != nil && s.Bounds.Offset != nil) {
		return false
	}
	for node := project.Child; node != nil; {
		switch n := node.(type) {
		case *plan.SetProjectNode:
			return false // Target sets, including terminal attempts, own cardinality.
		case *plan.SetNode, *plan.RecursiveUnionNode:
			return false // Set keys determine presence, not just projected payload.
		case *plan.SourceNode, *plan.SingletonNode, *plan.ValuesNode, *plan.DerivedNode, *plan.WorktableScanNode, *plan.JoinNode, *plan.FunctionFromNode:
			return true // FROM inputs retain their own demand and clause fences.
		case *plan.WindowNode:
			return false // Keep real window-key demand and eager failure frontiers.
		case *plan.AggregateNode:
			if !n.DiscardableGrouping() {
				return false
			}
		case *plan.RowLockNode:
			return false
		case *plan.LimitNode:
			if n.Offset != nil {
				return false
			}
			if n.Count != nil {
				expr := n.Count
				for {
					c, ok := expr.(plan.CoercionExpr)
					if !ok {
						break
					}
					expr = c.Child
				}
				literal, ok := expr.(plan.LiteralExpr)
				if !ok {
					return false
				}
				if !literal.Value.IsNull() {
					count, err := literal.Value.Integer()
					if err != nil || count <= 0 {
						return false
					}
				}
			}
		}
		left, right := queryInputs(node)
		if right != nil {
			break
		} // FROM inputs are not the root's clause fences.
		node = left
	}
	return true
}

// Presence demands keep the fully analyzed graphs while dropping root target,
// plain grouping, DISTINCT and ORDER demand for eligible EXISTS only.
func presenceQueryPlan(query *plan.Query) (*plan.Query, error) {
	project := query.Root.(*plan.ProjectNode)
	expressions := project.Expressions
	for i, bound := range project.Expressions {
		cached, ok := bound.(plan.CachedOrderExpr)
		if !ok {
			continue
		}
		original := scheduledQueryTarget(project.Child, cached.Slot)
		if original == nil {
			return nil, fmt.Errorf("stuntgres: missing analyzed presence target")
		}
		if &expressions[0] == &project.Expressions[0] {
			expressions = append([]expr.Expr(nil), expressions...)
		}
		expressions[i] = original
	}
	child := presenceQueryInput(project.Child)
	if child == project.Child && (len(expressions) == 0 || &expressions[0] == &project.Expressions[0]) {
		return query, nil
	}
	copy := *project
	copy.Child, copy.Expressions = child, expressions
	plan := *query
	plan.Root = &copy
	return &plan, nil
}

func scheduledQueryTarget(node plan.Node, slot int) expr.Expr {
	for node != nil {
		switch n := node.(type) {
		case *plan.EvaluationNode:
			if slot >= n.Base && slot-n.Base < len(n.Expressions) {
				return n.Expressions[slot-n.Base]
			}
		case *plan.SortNode:
			base := n.CacheWidth - len(n.Outputs)
			if slot >= base && slot-base < len(n.Outputs) {
				return n.Outputs[slot-base]
			}
			for _, key := range n.Keys {
				if key.Cache == slot {
					return key.Expression
				}
			}
		}
		switch n := node.(type) {
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.SortNode:
			node = n.Child
		case *plan.FilterNode:
			node = n.Child
		case *plan.AggregateNode:
			node = n.Child
		case *plan.WindowNode:
			node = n.Child
		case *plan.SetProjectNode:
			node = n.Child
		case *plan.DistinctNode:
			node = n.Child
		case *plan.DistinctOnNode:
			node = n.Child
		case *plan.RowLockNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		default:
			return nil
		}
	}
	return nil
}

func presenceQueryInput(node plan.Node) plan.Node {
	switch n := node.(type) {
	case *plan.EvaluationNode:
		return presenceQueryInput(n.Child)
	case *plan.SortNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.DistinctNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.DistinctOnNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.FilterNode:
		child := presenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.AggregateNode:
		if n.DiscardableGrouping() {
			copy := *n
			copy.Child = presenceQueryInput(n.Child)
			copy.PresenceOnly = true
			return &copy
		}
		child := presenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.RowLockNode:
		child := presenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.LimitNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	default:
		return node
	}
}

// A captured source distinguishes aliases of the same frozen plan. Both remain
// stable across unrelated parent rescans, within one actual execution owner.
type subqueryMembershipKey struct {
	plan   *plan.Query
	source *querySource
}

type subqueryMembership struct {
	flat     []sqltype.Value
	nullRows []int
	buckets  map[uint64][]int
	scratch  []sqltype.Value
	full     bool
}

func (c *EvalContext) subqueryMembership(e plan.SubqueryExpr) (membership *subqueryMembership, err error) {
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := stage.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = c.Context.Err()
		}
		if err != nil {
			membership = nil
		}
	}()
	width := len(e.Membership)
	membership = &subqueryMembership{}
	var small [4]sqltype.Value
	row := small[:min(width, len(small))]
	if width > len(small) {
		row = make([]sqltype.Value, width)
	}
	for {
		present, failure := stage.Next(c.Context, row)
		if failure != nil {
			return nil, failure
		}
		if !present {
			return membership, c.Context.Err()
		}
		if membership.scratch == nil {
			membership.scratch = make([]sqltype.Value, width)
			membership.buckets = make(map[uint64][]int)
		}
		rightContext := state.evaluation(c.Context)
		rightContext.Row = row
		hash := uint64(14695981039346656037)
		hasNull := false
		for i, comparison := range e.Comparisons {
			value, failure := comparison.Right.Eval(&rightContext)
			if failure != nil {
				return nil, failure
			}
			value, failure = e.Membership[i].PrepareKey(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			membership.scratch[i] = value
			hasNull = hasNull || value.IsNull()
			h, failure := e.Membership[i].Hash(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		count := len(membership.flat) / width
		if !hasNull {
			membership.full = true
			index, failure := findDistinctTuple(c.Context, e.Membership, membership.scratch, membership.flat, membership.buckets[hash])
			if failure != nil {
				return nil, failure
			}
			if index >= 0 {
				continue
			}
			membership.buckets[hash] = append(membership.buckets[hash], count)
		} else {
			membership.nullRows = append(membership.nullRows, count)
		}
		membership.flat = append(membership.flat, membership.scratch...)
	}
}

func (c *EvalContext) subqueryHashMembership(e plan.SubqueryExpr) (sqltype.Value, error) {
	owner := c.query.membershipOwner()
	key := subqueryMembershipKey{e.QueryPlan, c.query.source}
	membership := owner.memberships[key]
	if membership == nil {
		var err error
		membership, err = c.subqueryMembership(e)
		if err != nil {
			return sqltype.Value{}, err
		}
		if owner.memberships == nil {
			owner.memberships = make(map[subqueryMembershipKey]*subqueryMembership)
		}
		owner.memberships[key] = membership
	}
	// Empty RHS truth is known before any LHS projection, provider or effect.
	if len(membership.flat) == 0 {
		return sqltype.Boolean(e.Usage.Type, e.Negate), nil
	}
	left := membership.scratch
	leftNull := false
	for i, comparison := range e.Comparisons {
		value, err := comparison.Left.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		left[i], err = e.Membership[i].PrepareKey(c.Context, value)
		if err != nil {
			return sqltype.Value{}, err
		}
		leftNull = leftNull || left[i].IsNull()
	}
	if membership.full && !leftNull {
		hash := uint64(14695981039346656037)
		for i, value := range left {
			h, err := e.Membership[i].Hash(c.Context, value)
			if err != nil {
				return sqltype.Value{}, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index, err := findDistinctTuple(c.Context, e.Membership, left, membership.flat, membership.buckets[hash])
		if err != nil {
			return sqltype.Value{}, err
		}
		if index >= 0 {
			return sqltype.Boolean(e.Usage.Type, !e.Negate), nil
		}
	}
	// Keep NULL row indexes immutable: every probe needs the same partial-match
	// evidence. A decisive unequal nonNULL field can still establish FALSE.
	width := len(e.Membership)
	candidates := len(membership.nullRows)
	if leftNull {
		candidates = len(membership.flat) / width
	}
	for candidate := 0; candidate < candidates; candidate++ {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		index := candidate
		if !leftNull {
			index = membership.nullRows[candidate]
		}
		right := membership.flat[index*width : (index+1)*width]
		unknown, unequal := false, false
		for i, comparison := range e.Comparisons {
			value, err := comparison.Predicate.Evaluate(c.Context, left[i], right[i], c.Input)
			if err != nil {
				return sqltype.Value{}, err
			}
			if value.IsNull() {
				unknown = true
				continue
			}
			truth, err := value.Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
			if !truth {
				unequal = true
				break
			}
		}
		if unknown && !unequal {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	return sqltype.Boolean(e.Usage.Type, e.Negate), nil
}
