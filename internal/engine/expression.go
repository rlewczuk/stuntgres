package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Volatility uint8

const (
	Immutable Volatility = iota
	Stable
	Volatile
)

type ExprProperties struct {
	Volatility                                       Volatility
	RowDependent, ParameterDependent, StageDependent bool
}

// Dependence and volatility propagate independently through bound expressions.
type BoundExpr interface {
	boundExpression()
	ResultUsage() sqltype.Usage
	SourcePosition() parser.Position
	Properties() ExprProperties
	Eval(*EvalContext) (sqltype.Value, error)
}
type EvalContext struct {
	Context        context.Context
	Scope, Frame   uint32
	Row            []sqltype.Value
	Parameters     []sqltype.Value
	orderValues    []sqltype.Value
	statementScope *StatementScope
	Input          sqltype.InputEnvironment
	Output         sqltype.OutputEnvironment
}
type expressionInfo struct {
	usage      sqltype.Usage
	pos        parser.Position
	properties ExprProperties
}

func (expressionInfo) boundExpression()                  {}
func (e expressionInfo) ResultUsage() sqltype.Usage      { return e.usage }
func (e expressionInfo) SourcePosition() parser.Position { return e.pos }
func (e expressionInfo) Properties() ExprProperties      { return e.properties }
func evaluationContext(c *EvalContext) error {
	if c == nil || c.Context == nil {
		return fmt.Errorf("stuntgres: missing expression context")
	}
	return c.Context.Err()
}

type literalExpression struct {
	expressionInfo
	value sqltype.Value
}

func (e literalExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.usage.Type == nil || !e.usage.Type.Concrete() || e.value.Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid literal expression")
	}
	return e.value, nil
}

type columnExpression struct {
	expressionInfo
	origin       ObjectRef
	scope, frame uint32
	slot         int
}

func (e columnExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.Scope != e.scope || c.Frame != e.frame || e.slot < 0 || e.slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression row frame")
	}
	v := c.Row[e.slot]
	if e.usage.Type == nil || !e.usage.Type.Concrete() || v.Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression column type")
	}
	return v, nil
}

type parameterExpression struct {
	expressionInfo
	number int
}

func (e parameterExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.number <= 0 || e.number > len(c.Parameters) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter frame")
	}
	v := c.Parameters[e.number-1]
	if e.usage.Type == nil || !e.usage.Type.Concrete() || v.Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter type")
	}
	return v, nil
}

type coercionExpression struct {
	expressionInfo
	child BoundExpr
	plan  sqltype.CoercionPlan
	mode  sqltype.CoercionContext
}

func (e coercionExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	v, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.plan.Apply(c.Context, v, c.Input, c.Output)
}
func coercionProperties(child BoundExpr, plan sqltype.CoercionPlan) ExprProperties {
	properties := child.Properties()
	if plan.SessionDependent() && properties.Volatility < Stable {
		properties.Volatility = Stable
	}
	return properties
}

type comparisonExpression struct {
	expressionInfo
	left, right BoundExpr
	predicate   sqltype.ComparisonPredicate
	operator    string
	namespace   string
}

func (e comparisonExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.predicate.Evaluate(c.Context, a, b, c.Input)
}

// preflightCoercions materializes immutable conversions from the imported
// execution frame, never rows or stages. Only changed branches are copied;
// analyzed nodes and their conservative dependence metadata remain immutable.
func preflightCoercions(c *EvalContext, expression BoundExpr) (BoundExpr, bool, error) {
	return preflightCoercionsWithPolicy(c, expression, false)
}

// Only consumed stored defaults use decisive constant planning. Ordinary source,
// DELETE and supplied-value preflight retain their existing traversal demand.
func preflightCoercionsWithPolicy(c *EvalContext, expression BoundExpr, decisiveDefaults bool) (BoundExpr, bool, error) {
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	switch e := expression.(type) {
	case currentSchemasExpression:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.arg, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if changed {
			e.arg = arg
		}
		return e, changed, nil
	case caseExpression:
		return simplifyConditional(c, expression, decisiveDefaults)
	case conditionalExpression:
		if e.kind == parser.Coalesce {
			return simplifyConditional(c, expression, decisiveDefaults)
		}
		var args []BoundExpr
		for i, arg := range e.args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case timeZoneExpression:
		value, changed, err := preflightCoercionsWithPolicy(c, e.value, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.value = value
		if !e.local {
			zone, zoneChanged, err := preflightCoercionsWithPolicy(c, e.zone, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			e.zone = zone
			changed = changed || zoneChanged
		}
		return e, changed, nil
	case coercionExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.child = child
		p := e.Properties()
		if p.Volatility == Immutable && !p.RowDependent && !p.StageDependent {
			value, err := e.Eval(c)
			return preflightValue(c, e.expressionInfo, value, err)
		}
		if changed {
			return e, true, nil
		}
	case comparisonExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(literalExpression)
			_, rightConstant := right.(literalExpression)
			if leftConstant && rightConstant {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case bitwiseExpression:
		left, lc, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right BoundExpr
		var rc bool
		if e.right != nil {
			right, rc, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.left, e.right = left, right
		if bitwiseConstantNull(left) || bitwiseConstantNull(right) {
			return preflightValue(c, e.expressionInfo, sqltype.Null(e.usage.Type), nil)
		}
		if decisiveDefaults {
			_, l := left.(literalExpression)
			_, r := right.(literalExpression)
			p := e.Properties()
			if l && (right == nil || r) && p == (ExprProperties{}) {
				v, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, v, err)
			}
		}
		if lc || rc {
			return e, true, nil
		}
	case arithmeticExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right BoundExpr
		var rightChanged bool
		if e.right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		if decisiveDefaults {
			_, leftConstant := left.(literalExpression)
			_, rightConstant := right.(literalExpression)
			p := e.Properties()
			if leftConstant && (e.right == nil || rightConstant) && p.Volatility == Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		// Ordinary consumers only materialize casts; arithmetic stays row-owned.
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case nullTestExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(literalExpression); constant {
				e.child = child
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if changed {
			e.child = child
			return e, true, nil
		}
	case truthTestExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(literalExpression); constant {
				e.child = child
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if changed {
			e.child = child
			return e, true, nil
		}
	case distinctExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(literalExpression)
			_, rightConstant := right.(literalExpression)
			if leftConstant && rightConstant {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case logicalExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var leftConstant bool
		if decisiveDefaults {
			literal, constant := left.(literalExpression)
			leftConstant = constant
			if constant && literal.value.Type() != nil && literal.value.Type().Concrete() &&
				literal.value.Type().ID() == sqltype.Bool && !literal.value.IsNull() {
				truth, err := TruthOf(literal.value)
				if err != nil {
					return nil, false, err
				}
				if (e.op == parser.LogicalOr && truth == True) || (e.op == parser.LogicalAnd && truth == False) {
					return preflightValue(c, e.expressionInfo, literal.value, nil)
				}
			}
		}
		var right BoundExpr
		var rightChanged bool
		if e.right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		if decisiveDefaults && leftConstant {
			_, rightConstant := right.(literalExpression)
			if e.op == parser.LogicalNot || rightConstant {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case sequenceCallExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercions(c, arg)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case mathExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case randomExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case nullCountExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	}
	return expression, false, nil
}

func preflightValue(c *EvalContext, info expressionInfo, value sqltype.Value, err error) (BoundExpr, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	return literalExpression{expressionInfo: info, value: value}, true, nil
}

type Truth uint8

const (
	False Truth = iota
	True
	Unknown
)

func TruthOf(v sqltype.Value) (Truth, error) {
	if v.Type() == nil || v.Type().ID() != sqltype.Bool {
		return False, fmt.Errorf("stuntgres: predicate is not typed boolean")
	}
	if v.IsNull() {
		return Unknown, nil
	}
	b, err := v.Boolean()
	if err != nil {
		return False, err
	}
	if b {
		return True, nil
	}
	return False, nil
}

type expressionContext uint8

const (
	projectionContext expressionContext = iota
	filterContext
	assignmentContext
	functionArgumentContext
)

type expressionPolicy struct {
	context          expressionContext
	expected         sqltype.Usage
	mode             sqltype.CoercionContext
	column           string
	booleanConstruct string
	// Operand skips the enclosing WHERE BOOL check while binding predicate children.
	operand bool
	// Only selected bitwise operands propagate this through standard cast chains.
	bitwiseOperand bool
}
type expressionDependency struct {
	ref      ObjectRef
	revision DefinitionRevision
}
type expressionBinder struct {
	engine            *Engine
	ctx               context.Context
	relation          *relationDescriptor
	ref               ObjectRef
	parameters        map[int]sqltype.Usage
	updateParameters  bool
	uses              []int
	dependencies      []expressionDependency
	input             sqltype.InputEnvironment
	op                *Operation
	expected          *StatementDescription
	temporals         []temporalConstant
	anyParameters     []parameterExpression
	deleteTarget      bool
	targetName        string
	targetAlias       string
	defaultConsumer   bool
	sequenceConstants []sequenceConstant
	aggregateClause   string
	aggregates        []aggregateProvider
	queryAuth         *AuthorizationView
	querySources      []queryLeaf
	queryNamespace    *queryNamespace
	queryHidden       []queryQualifier
	noParameters      bool
	viewLiterals      map[viewLiteralPath]literalExpression
	viewColumns       map[viewLiteralPath]int
	viewStars         map[int][]int
	viewJoinCount     int
	viewNamespaces    []viewNamespace
}

func newExpressionBinder(ctx context.Context, e *Engine, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) *expressionBinder {
	return &expressionBinder{engine: e, ctx: ctx, parameters: make(map[int]sqltype.Usage), input: input, op: op, expected: expected}
}
func (b *expressionBinder) typ(id sqltype.ID) *sqltype.Descriptor {
	d, _ := (CatalogRead{owner: b.engine, state: b.engine.state}).LookupType(id)
	return d
}
func (b *expressionBinder) dependency(ref ObjectRef, revision DefinitionRevision) {
	for _, d := range b.dependencies {
		if d.ref == ref {
			return
		}
	}
	b.dependencies = append(b.dependencies, expressionDependency{ref, revision})
}
func (b *expressionBinder) info(u sqltype.Usage, pos parser.Position, p ExprProperties) expressionInfo {
	b.dependency(ObjectRef{owner: b.engine.owner, Epoch: b.engine.state.epoch, Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}}, 1)
	return expressionInfo{u, pos, p}
}
func expressionError(code, message string, pos parser.Position) error {
	d := sqlError(code, message).(*sqlstate.Error)
	if pos.Character > 0 {
		d.Position = strconv.Itoa(pos.Character)
	}
	return d
}
func (b *expressionBinder) constrain(n int, u sqltype.Usage, pos parser.Position) error {
	if n <= 0 {
		return expressionError("42P02", fmt.Sprintf("there is no parameter $%d", n), pos)
	}
	if old, ok := b.parameters[n]; ok && old.Type != nil && old.Type != u.Type {
		if b.updateParameters {
			// UPDATE uses occurrence-sensitive inference diagnostics; other
			// assignment consumers retain their selected 0A000 boundary.
			_, err := b.coerceParameter(parameterExpression{expressionInfo: expressionInfo{pos: pos}, number: n}, u)
			return err
		}
		return sqlError("0A000", "parameter reused with different destination types")
	}
	b.parameters[n] = u.Type.Usage()
	return nil
}
func (b *expressionBinder) finish() (ParameterInfo, error) {
	// Retained unknown ANY and null-test occurrences share binding order.
	// Diagnose occurrence mismatches before gaps or unresolved shared slots.
	for _, p := range b.anyParameters {
		if b.parameters[p.number].Type != nil {
			return ParameterInfo{}, expressionError("42P08", fmt.Sprintf("could not determine data type of parameter $%d", p.number), p.pos)
		}
	}
	keys := make([]int, 0, len(b.parameters))
	for n := range b.parameters {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for i, n := range keys {
		if n != i+1 || b.parameters[n].Type == nil {
			return ParameterInfo{}, sqlError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	info := ParameterInfo{Count: len(keys), Types: make([]sqltype.Usage, len(keys))}
	for i, n := range keys {
		info.Types[i] = b.parameters[n]
	}
	return info, nil
}

// Output fallback belongs to an unresolved occurrence, not its subsequently
// constrained shared slot. These private provisional nodes never escape describe.
func (b *expressionBinder) finalizeProjection(node BoundExpr) (BoundExpr, error) {
	p, ok := node.(parameterExpression)
	if !ok || p.usage.Type != nil {
		return node, nil
	}
	return b.coerceParameter(p, b.typ(sqltype.Text).Usage())
}
func (b *expressionBinder) column(raw parser.ColumnExpr) (BoundExpr, error) {
	if b.queryNamespace != nil {
		return b.queryColumn(raw)
	}
	if len(raw.Name.Parts) == 2 && !b.deleteTarget {
		return nil, expressionError("0A000", "qualified column references are unsupported", raw.Pos)
	}
	if len(raw.Name.Parts) == 0 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.relation == nil {
		return nil, expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	if len(raw.Name.Parts) == 2 {
		qualifier := raw.Name.Parts[0].Text
		visible := b.targetName
		if b.targetAlias != "" {
			visible = b.targetAlias
		}
		if qualifier != visible {
			if qualifier == b.targetName && b.targetAlias != "" {
				err := expressionError("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, qualifier), raw.Pos).(*sqlstate.Error)
				err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.targetAlias)
				return nil, err
			}
			return nil, expressionError("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, qualifier), raw.Pos)
		}
	}
	i, ok := b.relation.index[name]
	if !ok {
		if len(raw.Name.Parts) == 2 {
			return nil, expressionError("42703", fmt.Sprintf("column %s.%s does not exist", raw.Name.Parts[0].Text, name), raw.Pos)
		}
		return nil, expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.columnAt(i, raw.Pos), nil
}
func (b *expressionBinder) useColumn(i int) ObjectRef {
	ref := b.ref
	ref.Address.SubID = b.relation.attributes[i]
	b.dependency(ref, b.relation.revision)
	used := false
	for _, slot := range b.uses {
		if slot == i {
			used = true
			break
		}
	}
	if !used {
		b.uses = append(b.uses, i)
	}
	b.info(b.relation.columns[i].Usage, parser.Position{Offset: -1}, ExprProperties{})
	return ref
}
func (b *expressionBinder) columnAt(i int, pos parser.Position) BoundExpr {
	ref := b.useColumn(i)
	return columnExpression{expressionInfo: expressionInfo{usage: b.relation.columns[i].Usage, pos: pos, properties: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: i}
}
func (b *expressionBinder) bind(raw parser.Expr, policy expressionPolicy) (BoundExpr, error) {
	if policy.context == filterContext && !policy.operand && policy.booleanConstruct == "" {
		return b.bindBoolean(raw, policy.context, "WHERE")
	}
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("stuntgres: missing scalar expression")
	}
	var node BoundExpr
	switch r := raw.(type) {
	case parser.AggregateExpr:
		if b.aggregateClause != "SELECT" && b.aggregateClause != "HAVING" && b.aggregateClause != "ORDER BY" {
			if b.aggregateClause == "WHERE" || b.aggregateClause == "GROUP BY" || b.aggregateClause == "JOIN conditions" {
				return nil, expressionError("42803", "aggregate functions are not allowed in "+b.aggregateClause, r.Pos)
			}
			return nil, expressionError("0A000", "aggregate calls outside SELECT are unsupported", r.Pos)
		}
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: r.Name, Pos: r.Pos}}}
		if r.Schema != nil {
			name.Parts = []parser.Identifier{*r.Schema, {Text: r.Name, Pos: r.Pos}}
		}
		visible, err := b.builtinVisible(name, functionLookup)
		if err != nil {
			return nil, err
		}
		if !visible {
			return nil, b.sequenceSignatureError(parser.CallExpr{Name: name})
		}
		provider, ok := b.aggregateProvider(r.Name, r.Star)
		if !ok {
			return nil, expressionError("0A000", "aggregate call is unsupported", r.Pos)
		}
		if len(b.aggregates) == 0 {
			b.aggregates = append(b.aggregates, provider)
		}
		node = aggregateExpression{b.info(provider.ResultUsage(), r.Pos, ExprProperties{StageDependent: true}), 0}
	case parser.CaseExpr:
		var err error
		node, err = b.bindCase(r)
		if err != nil {
			return nil, err
		}
	case parser.ConditionalExpr:
		var err error
		node, err = b.bindConditional(r)
		if err != nil {
			return nil, err
		}
	case parser.FunctionExpr:
		var err error
		node, err = b.bindFunction(r)
		if err != nil {
			return nil, err
		}
	case parser.CallExpr:
		var err error
		node, err = b.bindSequenceCall(r)
		if err != nil {
			return nil, err
		}
	case parser.RegclassExpr:
		var err error
		node, _, err = b.bindRegclass(r.Child)
		if err != nil {
			return nil, err
		}
	case parser.ColumnExpr:
		if b.defaultConsumer {
			return nil, expressionError("0A000", "cannot use column reference in DEFAULT expression", r.Pos)
		}
		if policy.context == assignmentContext {
			return nil, expressionError("0A000", "column references outside SELECT are unsupported", r.Pos)
		}
		var err error
		node, err = b.column(r)
		if err != nil {
			return nil, err
		}
	case parser.ParameterExpr:
		if b.noParameters || b.defaultConsumer || r.Number <= 0 {
			return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
		}
		u := policy.expected
		if u.Type == nil {
			u = b.parameters[r.Number]
		}
		if u.Type == nil {
			b.parameters[r.Number] = sqltype.Usage{}
			node = parameterExpression{expressionInfo{pos: r.Pos, properties: ExprProperties{ParameterDependent: true}}, r.Number}
		} else {
			if err := b.constrain(r.Number, u, r.Pos); err != nil {
				return nil, err
			}
			node = parameterExpression{b.info(u.Type.Usage(), r.Pos, ExprProperties{ParameterDependent: true}), r.Number}
		}
	case parser.LiteralExpr:
		v := r.Value
		u := policy.expected
		var value sqltype.Value
		var err error
		switch v.Kind {
		case parser.NullValue:
			if u.Type == nil {
				u = b.typ(sqltype.Text).Usage()
			}
			value = sqltype.Null(u.Type)
		case parser.StringValue:
			if u.Type == nil {
				u = b.typ(sqltype.Text).Usage()
			}
			value, err = b.inputConstant(v.Text, u.Type.Usage(), v.Pos)
		case parser.NumericValue:
			value, err = b.inputConstant(v.Text, b.typ(sqltype.Numeric).Usage(), v.Pos)
		case parser.IntegerValue:
			id := sqltype.Int4
			if v.Integer < -2147483648 || v.Integer > 2147483647 {
				id = sqltype.Int8
			}
			value, err = sqltype.Integer(b.typ(id), v.Integer)
		case parser.BooleanValue:
			value = sqltype.Boolean(b.typ(sqltype.Bool), v.Boolean)
		default:
			return nil, fmt.Errorf("stuntgres: invalid scalar literal")
		}
		if err != nil {
			return nil, literalInputError(err, r)
		}
		node = literalExpression{b.info(value.Type().Usage(), v.Pos, ExprProperties{}), value}
	case parser.TypedLiteralExpr:
		rawType := r.TypeName
		saved, found, err := b.captureAt(r.Pos.Offset)
		if err != nil {
			return nil, err
		}
		if found {
			rawType.Modifiers = nil
		}
		u, err := b.engine.resolveType(rawType, b.op)
		if err != nil {
			return nil, err
		}
		if found {
			u.Typmod = saved.typmod
		}
		value, err := b.inputConstant(r.Text, u, r.Pos)
		if err != nil {
			return nil, inputErrorAt(err, r.Pos)
		}
		node = literalExpression{b.info(u, r.Pos, ExprProperties{}), value}
	case parser.CastExpr:
		var err error
		node, err = b.cast(r, policy.bitwiseOperand)
		if err != nil {
			return nil, err
		}
	case parser.TimeZoneExpr:
		var err error
		node, err = b.bindTimeZone(r, policy)
		if err != nil {
			return nil, err
		}
	case parser.LogicalExpr:
		var err error
		node, err = b.bindLogical(r, policy.context)
		if err != nil {
			return nil, err
		}
	case parser.PredicateExpr:
		if policy.context == assignmentContext {
			return nil, fmt.Errorf("stuntgres: predicate in assignment scalar")
		}
		var err error
		node, err = b.bindPredicate(r, policy.context)
		if err != nil {
			return nil, err
		}
	case parser.OperatorExpr:
		var err error
		if len(r.Name.Parts) > 0 && bitwiseName(r.Name.Parts[len(r.Name.Parts)-1].Text) {
			node, err = b.bindBitwise(r)
		} else if len(r.Name.Parts) > 0 && (arithmeticName(r.Name.Parts[len(r.Name.Parts)-1].Text) || symbolicArithmeticName(r.Name.Parts[len(r.Name.Parts)-1].Text)) {
			node, err = b.bindArithmetic(r)
		} else {
			if policy.context == assignmentContext {
				return nil, fmt.Errorf("stuntgres: comparison in assignment scalar")
			}
			node, err = b.bindComparison(r)
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("stuntgres: unsupported scalar node")
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.ID() != sqltype.Bool {
		return nil, expressionError("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", policy.booleanConstruct, node.ResultUsage().Type.DiagnosticName()), parser.ExpressionStart(raw))
	}
	if (policy.context == assignmentContext || policy.context == functionArgumentContext) && node.ResultUsage() != policy.expected {
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), policy.expected, policy.mode)
		if err != nil {
			// Retain the resolved source for the assignment caller's diagnostic.
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && policy.column != "" {
				copy := *d
				copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", policy.column, policy.expected.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())
				copy.Hint = "You will need to rewrite or cast the expression."
				copy.Position = strconv.Itoa(parser.ExpressionStart(raw).Character)
				return node, &copy
			}
			return node, err
		}
		node = coercionExpression{b.info(policy.expected, raw.SourcePosition(), coercionProperties(node, plan)), node, plan, policy.mode}
	}
	return node, nil
}

const functionCastHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func functionCastError(raw parser.CastExpr, source string, ambiguous bool) error {
	code, ending, hint := "42883", "does not exist", functionCastHint
	if ambiguous {
		code, ending = "42725", "is not unique"
		hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	name := raw.Callee
	if raw.TypeName.Schema != nil {
		name = raw.TypeName.Schema.Text + "." + name
	}
	d := expressionError(code, fmt.Sprintf("function %s(%s) %s", name, source, ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *expressionBinder) cast(raw parser.CastExpr, bitwiseOperand bool) (BoundExpr, error) {
	if !b.defaultConsumer && b.queryNamespace == nil {
		if err := qualifiedCastChild(raw.Child); err != nil {
			return nil, err
		}
	}
	visible := true
	if raw.Syntax == parser.FunctionCast {
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: raw.Callee, Pos: raw.Pos}}}
		if raw.TypeName.Schema != nil {
			name.Parts = []parser.Identifier{*raw.TypeName.Schema, {Text: raw.Callee, Pos: raw.Pos}}
		}
		var err error
		visible, err = b.builtinVisible(name, functionLookup)
		if err != nil {
			return nil, err
		}
	}
	if raw.Syntax == parser.FunctionCast && (!visible || raw.Callee != "oid" && raw.Callee != "int4" && raw.Callee != "int8" && raw.Callee != "text" && raw.Callee != "bool") {
		source := "unknown"
		if literal, ok := raw.Child.(parser.LiteralExpr); !ok || (literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue) {
			child, err := b.bind(raw.Child, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
			if child.ResultUsage().Type != nil {
				source = child.ResultUsage().Type.DiagnosticName()
			}
		}
		return nil, functionCastError(raw, source, false)
	}
	target, err := b.engine.resolveType(raw.TypeName, b.op)
	if err != nil {
		return nil, err
	}
	if target.Type.ID() == sqltype.Regclass {
		node, _, err := b.bindRegclass(raw.Child)
		return node, err
	}
	bitwiseCast := raw.Syntax != parser.FunctionCast && (bitwiseOperand || bitwiseCastSource(raw.Child))
	if !sqltype.BaselineCastType(target.Type.ID()) && target.Type.ID() != sqltype.Oid && target.Type.ID() != sqltype.Int2 && !sqltype.IsCharacter(target.Type.ID()) && !sqltype.TemporalCastType(target.Type.ID()) {
		return nil, expressionError("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	policy := expressionPolicy{context: projectionContext, bitwiseOperand: bitwiseOperand}
	switch child := raw.Child.(type) {
	case parser.LiteralExpr:
		if child.Value.Kind == parser.StringValue || child.Value.Kind == parser.NullValue {
			policy.expected = target
		}
	case parser.ParameterExpr:
		if b.noParameters || b.defaultConsumer {
			return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
		}
		if b.parameters[child.Number].Type == nil {
			if raw.Syntax == parser.FunctionCast && target.Type.ID() != sqltype.Text {
				if child.Number <= 0 {
					return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
				}
				return nil, functionCastError(raw, "unknown", true)
			}
			policy.expected = target
		}
	}
	child, err := b.bind(raw.Child, policy)
	if err != nil {
		return nil, err
	}
	source := child.ResultUsage()
	scopedIntegral := bitwiseCast && integralBitwiseType(source.Type) && integralBitwiseType(target.Type)
	var plan sqltype.CoercionPlan
	if scopedIntegral {
		plan, err = planExpressionCoercion(source, target, sqltype.Explicit)
	} else {
		plan, err = sqltype.PlanCoercion(source, target, sqltype.Explicit)
	}
	if err != nil {
		if !scopedIntegral && (!sqltype.BaselineCastType(source.Type.ID()) || !sqltype.BaselineCastType(target.Type.ID())) && !sqltype.OIDCastPair(source.Type.ID(), target.Type.ID()) && !sqltype.TemporalCastPair(source.Type.ID(), target.Type.ID()) {
			return nil, expressionError("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
		}
		if raw.Syntax == parser.FunctionCast {
			return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
		}
		return nil, expressionError("42846", fmt.Sprintf("cannot cast type %s to %s", source.Type.DiagnosticName(), target.Type.DiagnosticName()), raw.Pos)
	}
	if source == target {
		return child, nil
	}
	pos := raw.Pos
	if start := child.SourcePosition(); start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
		pos = start
	}
	return coercionExpression{b.info(target, pos, coercionProperties(child, plan)), child, plan, sqltype.Explicit}, nil
}

// Strong column/function names survive standard casts; weak literal names do not.
func expressionName(raw parser.Expr, usage sqltype.Usage) (string, bool) {
	switch r := raw.(type) {
	case parser.AggregateExpr:
		return r.Name, true
	case parser.ConditionalExpr:
		return conditionalName(r), true
	case parser.CaseExpr:
		if r.Else != nil {
			if name, strong := expressionName(r.Else, usage); strong {
				return name, true
			}
		}
		return "case", false
	case parser.ColumnExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.FunctionExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.CallExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.RegclassExpr:
		return "regclass", false
	case parser.TimeZoneExpr:
		return "timezone", true
	case parser.CastExpr:
		if r.Syntax == parser.FunctionCast {
			return r.Callee, true
		}
		if name, strong := expressionName(r.Child, usage); strong {
			return name, true
		}
		return usage.Type.Name(), false
	case parser.TypedLiteralExpr:
		return usage.Type.Name(), false
	default:
		return "?column?", false
	}
}
func inputErrorAt(err error, pos parser.Position) error {
	if d, ok := err.(*sqlstate.Error); ok && (d.Code == "22P02" || d.Code == "22003" || d.Code == "22007" || d.Code == "22008" || d.Code == "22009" || d.Code == "22023" || d.Code == "42601") {
		copy := *d
		if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return &copy
	}
	return err
}
func literalInputError(err error, raw parser.LiteralExpr) error {
	return inputErrorAt(err, raw.Value.Pos)
}

// Explicit typed literals use this only to discover their frozen modifier
// before resolving the raw declaration again. Scalar reconstruction must use
// captured with the complete contextual usage.
func (b *expressionBinder) captureAt(offset int) (temporalConstant, bool, error) {
	if b.expected != nil {
		captures := b.expected.temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].offset >= offset })
		if i < len(captures) && captures[i].offset == offset {
			saved := captures[i]
			for i++; i < len(captures) && captures[i].offset == offset; i++ {
				if captures[i].typeID != saved.typeID || captures[i].typmod != saved.typmod {
					return temporalConstant{}, false, fmt.Errorf("stuntgres: ambiguous captured temporal metadata")
				}
			}
			return saved, true, nil
		}
	}
	return temporalConstant{}, false, nil
}

func (b *expressionBinder) captured(offset int, usage sqltype.Usage) (temporalConstant, bool, error) {
	if b.expected != nil {
		captures := b.expected.temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].offset >= offset })
		if i < len(captures) && captures[i].offset == offset {
			for ; i < len(captures) && captures[i].offset == offset; i++ {
				capture := captures[i]
				if capture.typeID == usage.Type.ID() && capture.typmod == usage.Typmod {
					return capture, true, nil
				}
			}
			return temporalConstant{}, false, fmt.Errorf("stuntgres: captured temporal usage changed")
		}
	}
	return temporalConstant{}, false, nil
}
func (b *expressionBinder) inputConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time && u.Type.ID() != sqltype.Date && u.Type.ID() != sqltype.Timestamptz && u.Type.ID() != sqltype.Timetz {
		return u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
	}
	c, found, err := b.captured(pos.Offset, u)
	if err != nil {
		return sqltype.Value{}, err
	}
	if found {
		if c.typeID == sqltype.Timetz {
			return sqltype.TimetzValue(u.Type, c.scalar, c.west)
		}
		if c.typeID == sqltype.Time {
			return sqltype.TimeValue(u.Type, c.scalar)
		}
		if c.typeID == sqltype.Date {
			return sqltype.DateValue(u.Type, c.scalar)
		}
		return sqltype.TimestampValue(u.Type, c.scalar)
	}
	v, err := u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
	if err == nil && u.Typmod >= 0 {
		var p sqltype.CoercionPlan
		p, err = sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Assignment)
		if err == nil {
			v, err = p.Apply(b.ctx, v, b.input, sqltype.OutputEnvironment{Zone: b.input.Zone, DateStyle: b.input.DateStyle})
		}
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if b.expected == nil {
		var n int64
		var err error
		if u.Type.ID() == sqltype.Time || u.Type.ID() == sqltype.Timetz {
			n, err = v.Time()
		} else if u.Type.ID() == sqltype.Date {
			n, err = v.Date()
		} else {
			n, err = v.Timestamp()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		// Symmetric comparison can contextualize an earlier unknown literal
		// after its right operand. Keep captures ordered for rebind lookup.
		i := sort.Search(len(b.temporals), func(i int) bool { return b.temporals[i].offset >= pos.Offset })
		b.temporals = append(b.temporals, temporalConstant{})
		copy(b.temporals[i+1:], b.temporals[i:])
		west := int32(0)
		if u.Type.ID() == sqltype.Timetz {
			west, err = v.TimeZoneOffset()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		b.temporals[i] = temporalConstant{offset: pos.Offset, scalar: n, typmod: u.Typmod, typeID: u.Type.ID(), west: west}
	}
	return v, nil
}
func operatorExpressionError(err error, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42883" {
		return err
	}
	copy := *d
	name := func(t *sqltype.Descriptor) string {
		if t == nil {
			return "unknown"
		}
		return t.DiagnosticName()
	}
	if len(raw.Args) == 1 {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s", objectNameText(raw.Name), name(left))
		copy.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
	} else {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", name(left), objectNameText(raw.Name), name(right))
		copy.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	}
	copy.Position = strconv.Itoa(raw.Pos.Character)
	return &copy
}

// Unknown state exists only during analysis. It never becomes an executable Value.
type comparisonOperand struct {
	node BoundExpr
	raw  parser.Expr
}

func (o comparisonOperand) descriptor() *sqltype.Descriptor {
	if o.node == nil {
		return nil
	}
	return o.node.ResultUsage().Type
}

func (b *expressionBinder) comparisonOperand(raw parser.Expr) (comparisonOperand, error) {
	return b.comparisonOperandPolicy(raw, expressionPolicy{context: projectionContext})
}

func (b *expressionBinder) comparisonOperandPolicy(raw parser.Expr, policy expressionPolicy) (comparisonOperand, error) {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return comparisonOperand{raw: raw}, nil
	}
	switch raw.(type) {
	case parser.ColumnExpr, parser.ParameterExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.CastExpr, parser.TimeZoneExpr, parser.OperatorExpr, parser.CallExpr, parser.RegclassExpr, parser.FunctionExpr, parser.AggregateExpr, parser.CaseExpr, parser.ConditionalExpr, parser.LogicalExpr, parser.PredicateExpr:
	default:
		return comparisonOperand{}, fmt.Errorf("stuntgres: invalid comparison operand")
	}
	node, err := b.bind(raw, policy)
	return comparisonOperand{node: node, raw: raw}, err
}

func (b *expressionBinder) coerceParameter(p parameterExpression, u sqltype.Usage) (BoundExpr, error) {
	if existing := b.parameters[p.number]; existing.Type != nil && existing.Type != u.Type {
		d := expressionError("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.number), p.pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("%s versus %s", existing.Type.DiagnosticName(), u.Type.DiagnosticName())
		return nil, d
	}
	b.parameters[p.number] = u.Type.Usage()
	return parameterExpression{b.info(u.Type.Usage(), p.pos, p.properties), p.number}, nil
}

func (b *expressionBinder) finalizeOperand(o comparisonOperand, typ *sqltype.Descriptor) (BoundExpr, error) {
	if o.descriptor() != nil {
		return o.node, nil
	}
	if p, ok := o.node.(parameterExpression); ok {
		return b.coerceParameter(p, typ.Usage())
	}
	if o.raw == nil {
		return nil, fmt.Errorf("stuntgres: missing unknown comparison operand")
	}
	return b.bind(o.raw, expressionPolicy{context: projectionContext, expected: typ.Usage()})
}

// coerceComparisonOperands applies the bounded implicit integer→OID edge for
// both ordinary and null-safe comparisons, after occurrence-sensitive unknown
// operand inference. The shared coercion node owns range checks and typed NULLs.
func (b *expressionBinder) coerceComparisonOperands(left, right BoundExpr) (BoundExpr, BoundExpr, error) {
	lt, rt := left.ResultUsage().Type, right.ResultUsage().Type
	integer := func(t *sqltype.Descriptor) bool {
		return t.ID() == sqltype.Int2 || t.ID() == sqltype.Int4 || t.ID() == sqltype.Int8
	}
	var child BoundExpr
	var target sqltype.Usage
	switch {
	case lt.ID() == sqltype.Oid && integer(rt):
		child, target = right, lt.Usage()
	case rt.ID() == sqltype.Oid && integer(lt):
		child, target = left, rt.Usage()
	default:
		return left, right, nil
	}
	plan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Implicit)
	if err != nil {
		return nil, nil, err
	}
	converted := coercionExpression{b.info(target, child.SourcePosition(), coercionProperties(child, plan)), child, plan, sqltype.Implicit}
	if lt.ID() == sqltype.Oid {
		right = converted
	} else {
		left = converted
	}
	return left, right, nil
}

func (b *expressionBinder) bindComparison(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid comparison expression")
	}
	var operator sqltype.ComparisonOperator
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "=":
		operator = sqltype.CompareEqual
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
		return nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	// Copy each occurrence's then-known parameter type before resolving either side.
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	lt, rt := left.descriptor(), right.descriptor()
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if lt == nil {
		lt = rt
	}
	if rt == nil {
		rt = lt
	}
	if lt == nil {
		lt, rt = b.typ(sqltype.Text), b.typ(sqltype.Text)
	}
	l, err := b.finalizeOperand(left, lt)
	if err != nil {
		return nil, err
	}
	r, err := b.finalizeOperand(right, rt)
	if err != nil {
		return nil, err
	}
	l, r, err = b.coerceComparisonOperands(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type, b.typ(sqltype.Bool))
	if err != nil {
		return nil, operatorExpressionError(err, left.descriptor(), right.descriptor(), raw)
	}
	p, rp := l.Properties(), r.Properties()
	p.RowDependent = p.RowDependent || rp.RowDependent
	p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
	p.StageDependent = p.StageDependent || rp.StageDependent
	if rp.Volatility > p.Volatility {
		p.Volatility = rp.Volatility
	}
	if predicate.SessionDependent() && p.Volatility < Stable {
		p.Volatility = Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return comparisonExpression{b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, p), l, r, predicate, raw.Name.Parts[len(raw.Name.Parts)-1].Text, namespace}, nil
}

type arithmeticExpression struct {
	expressionInfo
	left, right BoundExpr
	provider    sqltype.Arithmetic
	operator    sqltype.ArithmeticOperator
	namespace   string
}

func (e arithmeticExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.right == nil {
		return e.provider.Unary(c.Context, a)
	}
	v, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Binary(c.Context, a, v)
}
func arithmeticName(name string) bool {
	return name == "+" || name == "-" || name == "*" || name == "/" || name == "%"
}
func symbolicArithmeticName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for _, c := range name {
		if c != '+' && c != '-' && c != '*' && c != '/' && c != '%' {
			return false
		}
	}
	return true
}
func integerArithmeticType(t *sqltype.Descriptor) bool {
	return t != nil && (t.ID() == sqltype.Int4 || t.ID() == sqltype.Int8)
}
func (b *expressionBinder) bindArithmetic(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid arithmetic expression")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	// Unqualified numeric unary plus is identity; input errors belong to its
	// operand token, including ORDER expressions. Qualified calls use lookup.
	if name == "+" && len(raw.Args) == 1 && len(raw.Name.Parts) == 1 {
		if literal, ok := raw.Args[0].(parser.LiteralExpr); ok && literal.Value.Kind == parser.NumericValue {
			return b.bind(literal, expressionPolicy{context: projectionContext})
		}
	}
	operator := sqltype.ArithmeticAdd
	switch name {
	case "-":
		operator = sqltype.ArithmeticSubtract
	case "*":
		operator = sqltype.ArithmeticMultiply
	case "/":
		operator = sqltype.ArithmeticDivide
	case "%":
		operator = sqltype.ArithmeticRemainder
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	var right comparisonOperand
	if len(raw.Args) == 2 {
		right, err = b.comparisonOperand(raw.Args[1])
		if err != nil {
			return nil, err
		}
	}
	lt, rt := left.descriptor(), right.descriptor()
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if symbolicArithmeticName(name) {
		return nil, operatorExpressionError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	// BOOL/TEXT with an integer (or unary BOOL/TEXT) has a genuine missing signature.
	badSignature := func(t *sqltype.Descriptor) bool {
		return t != nil && (t.ID() == sqltype.Bool || t.ID() == sqltype.Text)
	}
	if (len(raw.Args) == 1 && badSignature(lt)) || (integerArithmeticType(lt) && badSignature(rt)) || (badSignature(lt) && integerArithmeticType(rt)) {
		return nil, operatorExpressionError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist"}, lt, rt, raw)
	}
	for _, t := range []*sqltype.Descriptor{lt, rt} {
		if t != nil && !integerArithmeticType(t) {
			return nil, expressionError("0A000", fmt.Sprintf("arithmetic for %s operands is unsupported", t.DiagnosticName()), raw.Pos)
		}
	}
	typ := lt
	if typ == nil {
		typ = rt
	}
	if typ == nil {
		return nil, expressionError("0A000", "integer arithmetic requires known integer or bigint operands", raw.Pos)
	}
	if rt != nil && rt.ID() == sqltype.Int8 {
		typ = rt
	}
	provider, err := sqltype.BindArithmetic(operator, typ, len(raw.Args) == 1)
	if err != nil {
		return nil, err
	}
	finish := func(o comparisonOperand) (BoundExpr, error) {
		source := o.descriptor()
		if source == nil {
			source = typ
		}
		n, err := b.finalizeOperand(o, source)
		if err != nil {
			return nil, err
		}
		if source != typ {
			plan, err := sqltype.PlanCoercion(n.ResultUsage(), typ.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			n = coercionExpression{b.info(typ.Usage(), n.SourcePosition(), coercionProperties(n, plan)), n, plan, sqltype.Implicit}
		}
		return n, nil
	}
	l, err := finish(left)
	if err != nil {
		return nil, err
	}
	var r BoundExpr
	p := l.Properties()
	if len(raw.Args) == 2 {
		r, err = finish(right)
		if err != nil {
			return nil, err
		}
		rp := r.Properties()
		if rp.Volatility > p.Volatility {
			p.Volatility = rp.Volatility
		}
		p.RowDependent = p.RowDependent || rp.RowDependent
		p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
		p.StageDependent = p.StageDependent || rp.StageDependent
	}
	// Contextual consumers use the cached expression start; signature errors
	// above retain the raw operator token.
	pos := raw.Pos
	for _, child := range [...]BoundExpr{l, r} {
		if child != nil {
			start := child.SourcePosition()
			if start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
				pos = start
			}
		}
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return arithmeticExpression{b.info(typ.Usage(), pos, p), l, r, provider, operator, namespace}, nil
}
