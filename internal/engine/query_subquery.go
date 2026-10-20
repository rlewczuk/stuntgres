package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Subquery expressions borrow captured sources and the existing execution owner.
// Correlated scans reopen; uncorrelated hashed occurrences retain a built RHS.
type subqueryExpression struct {
	expr.Info
	kind            parser.SubqueryKind
	queryPlan       *BoundQuery
	comparisons     []comparisonExpression
	ordering        []sqltype.Comparison
	membership      []sqltype.Grouping // Nonempty only for uncorrelated hashable =ANY/IN.
	operator        string
	negate          bool
	presenceOnly    bool
	cteDefinition   bool // Analysis-only graph edge, never expression demand.
	jsonArray       *sqlJSONArrayQuery
	leftExpansions  map[int]compositeExpansionBinding // Analysis-only source associations.
	leftConstructor expr.Expr                         // Analysis-only normalized constructor; never evaluated or a dependency child.
	erasedLeftType  sqltype.ID                        // Original outer identity, only for pruning frozen presentation dependencies.
	updateWidth     int                               // Nonzero only for the first recipe of an UPDATE tuple source.
}

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
	var leftExpansions map[int]compositeExpansionBinding
	var leftConstructor expr.Expr
	var erasedLeftType sqltype.ID
	if raw.Left != nil {
		var err error
		if row, ok := raw.Left.(parser.RowExpr); ok {
			left, leftExpansions, err = b.rowComparisonOperands(row)
		} else {
			var operand comparisonOperand
			operand, err = b.comparisonOperand(raw.Left)
			if constructor, ok := operand.node.(rowExpression); ok && constructor.raw != nil && !constructor.Usage.Type.IsDomain() {
				// A cast to a named composite can still analyze to a ROW
				// constructor. Quantified subqueries compare its fields;
				// a domain coercion or scalar subquery remains one value.
				left = make([]comparisonOperand, len(constructor.fields))
				for i, field := range constructor.fields {
					left[i] = comparisonOperand{node: field}
				}
				leftExpansions = constructor.expansions
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
	project := query.Root.(*projectNode)
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
		project = query.Root.(*projectNode)
	}
	properties := queryDemandProperties(project, presenceOnly)
	properties.StageDependent = true
	e := subqueryExpression{Info: b.info(usage, raw.Pos, properties), kind: raw.Kind, queryPlan: query, negate: raw.Negate, presenceOnly: presenceOnly}
	e.leftExpansions = leftExpansions
	e.leftConstructor, e.erasedLeftType = leftConstructor, erasedLeftType
	if raw.Kind == parser.SubqueryScalar || raw.Kind == parser.SubqueryExists {
		return e, nil
	}
	e.operator = raw.Operator.Parts[len(raw.Operator.Parts)-1].Text
	operator := sqltype.CompareEqual
	switch e.operator {
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
	e.comparisons = make([]comparisonExpression, width)
	if width > 1 && operator > sqltype.CompareNotEqual {
		e.ordering = make([]sqltype.Comparison, width)
	}
	for i, column := range project.Schema() {
		right := columnExpression{Info: b.info(column.Usage, raw.Pos, expr.Properties{RowDependent: true}), scope: 1, frame: project.frame, slot: i}
		bound, err := b.bindComparisonOperands(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, operator, left[i], comparisonOperand{node: right})
		if err != nil {
			return nil, err
		}
		e.comparisons[i] = bound.(comparisonExpression)
		e.Property = expr.MergeProperties(e.Property, e.comparisons[i].left.Properties())
		if len(e.ordering) != 0 {
			ref, conflict := e.comparisons[i].predicate.InputCollation()
			e.ordering[i], err = sqltype.BindComparison(e.comparisons[i].left.ResultUsage().Type, e.comparisons[i].right.ResultUsage().Type, ref, conflict)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := b.checkRowComparisonInterpretation(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, e.comparisons); err != nil {
		return nil, err
	}
	if raw.Kind == parser.SubqueryAny && operator == sqltype.CompareEqual && !properties.RowDependent {
		families := make([]sqltype.Grouping, width)
		for i, comparison := range e.comparisons {
			families[i], err = comparison.predicate.EqualityGrouping()
			if err != nil {
				families = nil
				break
			}
		}
		e.membership = families
	}
	return e, nil
}

func (e subqueryExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalSubquery(e)
}

func (c *EvalContext) EvalSubquery(e subqueryExpression) (value sqltype.Value, err error) {
	if err = evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.query == nil || c.query.source == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing subquery statement capture")
	}
	if e.updateWidth != 0 {
		return c.subqueryUpdateOutputs(e)
	}
	if len(e.membership) != 0 {
		return c.subqueryHashMembership(e)
	}
	var smallLeft [4]sqltype.Value
	left := smallLeft[:min(len(e.comparisons), len(smallLeft))]
	if len(e.comparisons) > len(smallLeft) {
		left = make([]sqltype.Value, len(e.comparisons))
	}
	for i, comparison := range e.comparisons {
		left[i], err = comparison.left.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.queryPlan.Root.(*projectNode))
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() {
		closeErr := stage.Close()
		if err == nil {
			err = closeErr
		}
	}()
	if e.jsonArray != nil {
		return c.subqueryJSONArray(e, &state, &stage)
	}
	if e.kind == parser.SubqueryExists && e.presenceOnly {
		_, present, failure := stage.nextOccurrence(c.Context)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		return sqltype.Boolean(e.Usage.Type, present), nil
	}
	if e.kind == parser.SubqueryExists {
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
	if e.kind == parser.SubqueryScalar {
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
			if e.kind == parser.SubqueryRow || unknown {
				return sqltype.Null(e.Usage.Type), nil
			}
			truth := e.kind == parser.SubqueryAll
			if e.negate {
				truth = !truth
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
		rightContext := state.evaluation(c.Context)
		rightContext.Row = row
		answer, failure := e.compare(&rightContext, left)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if e.kind == parser.SubqueryRow {
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
		if e.kind == parser.SubqueryAll && !truth || e.kind == parser.SubqueryAny && truth {
			if e.negate {
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
func canSimplifyExists(query *BoundQuery) bool {
	project := query.Root.(*projectNode)
	s := project.syntax
	if s != nil && (s.Having != nil || s.Locking != nil || s.Bounds != nil && s.Bounds.Offset != nil) {
		return false
	}
	for node := project.child; node != nil; {
		switch n := node.(type) {
		case *setProjectNode:
			return false // Target sets, including terminal attempts, own cardinality.
		case *setNode, *recursiveUnionNode:
			return false // Set keys determine presence, not just projected payload.
		case *sourceNode, *singletonNode, *valuesNode, *derivedNode, *worktableScanNode, *joinNode, *functionFromNode:
			return true // FROM inputs retain their own demand and clause fences.
		case *windowNode:
			return false // Keep real window-key demand and eager failure frontiers.
		case *aggregateNode:
			if !n.discardableGrouping() {
				return false
			}
		case *rowLockNode:
			return false
		case *limitNode:
			if n.offset != nil {
				return false
			}
			if n.count != nil {
				expr := n.count
				for {
					c, ok := expr.(coercionExpression)
					if !ok {
						break
					}
					expr = c.child
				}
				literal, ok := expr.(literalExpression)
				if !ok {
					return false
				}
				if !literal.value.IsNull() {
					count, err := literal.value.Integer()
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
func presenceQueryPlan(query *BoundQuery) (*BoundQuery, error) {
	project := query.Root.(*projectNode)
	expressions := project.expressions
	for i, bound := range project.expressions {
		cached, ok := bound.(cachedOrderExpression)
		if !ok {
			continue
		}
		original := scheduledQueryTarget(project.child, cached.slot)
		if original == nil {
			return nil, fmt.Errorf("stuntgres: missing analyzed presence target")
		}
		if &expressions[0] == &project.expressions[0] {
			expressions = append([]expr.Expr(nil), expressions...)
		}
		expressions[i] = original
	}
	child := presenceQueryInput(project.child)
	if child == project.child && (len(expressions) == 0 || &expressions[0] == &project.expressions[0]) {
		return query, nil
	}
	copy := *project
	copy.child, copy.expressions = child, expressions
	plan := *query
	plan.Root = &copy
	return &plan, nil
}

func scheduledQueryTarget(node LogicalNode, slot int) expr.Expr {
	for node != nil {
		switch n := node.(type) {
		case *evaluationNode:
			if slot >= n.base && slot-n.base < len(n.expressions) {
				return n.expressions[slot-n.base]
			}
		case *sortNode:
			base := n.cacheWidth - len(n.outputs)
			if slot >= base && slot-base < len(n.outputs) {
				return n.outputs[slot-base]
			}
			for _, key := range n.keys {
				if key.cache == slot {
					return key.expression
				}
			}
		}
		switch n := node.(type) {
		case *evaluationNode:
			node = n.child
		case *sortNode:
			node = n.child
		case *filterNode:
			node = n.child
		case *aggregateNode:
			node = n.child
		case *windowNode:
			node = n.child
		case *setProjectNode:
			node = n.child
		case *distinctNode:
			node = n.child
		case *distinctOnNode:
			node = n.child
		case *rowLockNode:
			node = n.child
		case *limitNode:
			node = n.child
		default:
			return nil
		}
	}
	return nil
}

func presenceQueryInput(node LogicalNode) LogicalNode {
	switch n := node.(type) {
	case *evaluationNode:
		return presenceQueryInput(n.child)
	case *sortNode:
		copy := *n
		copy.child = presenceQueryInput(n.child)
		copy.presenceOnly = true
		return &copy
	case *distinctNode:
		copy := *n
		copy.child = presenceQueryInput(n.child)
		copy.presenceOnly = true
		return &copy
	case *distinctOnNode:
		copy := *n
		copy.child = presenceQueryInput(n.child)
		copy.presenceOnly = true
		return &copy
	case *filterNode:
		child := presenceQueryInput(n.child)
		if child == n.child {
			return node
		}
		copy := *n
		copy.child = child
		return &copy
	case *aggregateNode:
		if n.discardableGrouping() {
			copy := *n
			copy.child = presenceQueryInput(n.child)
			copy.presenceOnly = true
			return &copy
		}
		child := presenceQueryInput(n.child)
		if child == n.child {
			return node
		}
		copy := *n
		copy.child = child
		return &copy
	case *rowLockNode:
		child := presenceQueryInput(n.child)
		if child == n.child {
			return node
		}
		copy := *n
		copy.child = child
		return &copy
	case *limitNode:
		copy := *n
		copy.child = presenceQueryInput(n.child)
		copy.presenceOnly = true
		return &copy
	default:
		return node
	}
}

// A captured source distinguishes aliases of the same frozen plan. Both remain
// stable across unrelated parent rescans, within one actual execution owner.
type subqueryMembershipKey struct {
	plan   *BoundQuery
	source *querySource
}

type subqueryMembership struct {
	flat     []sqltype.Value
	nullRows []int
	buckets  map[uint64][]int
	scratch  []sqltype.Value
	full     bool
}

func (c *EvalContext) subqueryMembership(e subqueryExpression) (membership *subqueryMembership, err error) {
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.queryPlan.Root.(*projectNode))
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
	width := len(e.membership)
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
		for i, comparison := range e.comparisons {
			value, failure := comparison.right.Eval(&rightContext)
			if failure != nil {
				return nil, failure
			}
			value, failure = e.membership[i].PrepareKey(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			membership.scratch[i] = value
			hasNull = hasNull || value.IsNull()
			h, failure := e.membership[i].Hash(c.Context, value)
			if failure != nil {
				return nil, failure
			}
			hash = (hash ^ h) * 1099511628211
		}
		count := len(membership.flat) / width
		if !hasNull {
			membership.full = true
			index, failure := findDistinctTuple(c.Context, e.membership, membership.scratch, membership.flat, membership.buckets[hash])
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

func (c *EvalContext) subqueryHashMembership(e subqueryExpression) (sqltype.Value, error) {
	owner := c.query.membershipOwner()
	key := subqueryMembershipKey{e.queryPlan, c.query.source}
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
		return sqltype.Boolean(e.Usage.Type, e.negate), nil
	}
	left := membership.scratch
	leftNull := false
	for i, comparison := range e.comparisons {
		value, err := comparison.left.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		left[i], err = e.membership[i].PrepareKey(c.Context, value)
		if err != nil {
			return sqltype.Value{}, err
		}
		leftNull = leftNull || left[i].IsNull()
	}
	if membership.full && !leftNull {
		hash := uint64(14695981039346656037)
		for i, value := range left {
			h, err := e.membership[i].Hash(c.Context, value)
			if err != nil {
				return sqltype.Value{}, err
			}
			hash = (hash ^ h) * 1099511628211
		}
		index, err := findDistinctTuple(c.Context, e.membership, left, membership.flat, membership.buckets[hash])
		if err != nil {
			return sqltype.Value{}, err
		}
		if index >= 0 {
			return sqltype.Boolean(e.Usage.Type, !e.negate), nil
		}
	}
	// Keep NULL row indexes immutable: every probe needs the same partial-match
	// evidence. A decisive unequal nonNULL field can still establish FALSE.
	width := len(e.membership)
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
		for i, comparison := range e.comparisons {
			value, err := comparison.predicate.Evaluate(c.Context, left[i], right[i], c.Input)
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
	return sqltype.Boolean(e.Usage.Type, e.negate), nil
}
