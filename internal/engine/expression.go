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

const Immutable Volatility = 0

type ExprProperties struct {
	Volatility                                       Volatility
	RowDependent, ParameterDependent, StageDependent bool
}

// Every admitted node is immutable. Dependence does not imply volatility.
type BoundExpr interface {
	boundExpression()
	ResultUsage() sqltype.Usage
	SourcePosition() parser.Position
	Properties() ExprProperties
	Eval(*EvalContext) (sqltype.Value, error)
}
type EvalContext struct {
	Context      context.Context
	Scope, Frame uint32
	Row          []sqltype.Value
	Parameters   []sqltype.Value
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
	return e.plan.Apply(c.Context, v)
}

type comparisonExpression struct {
	expressionInfo
	left, right BoundExpr
	predicate   sqltype.ComparisonPredicate
	operator    string
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
	return e.predicate.Evaluate(c.Context, a, b)
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
		// Only materialize child casts. Arithmetic itself remains row-owned.
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
)

type expressionPolicy struct {
	context          expressionContext
	expected         sqltype.Usage
	mode             sqltype.CoercionContext
	column           string
	booleanConstruct string
	// Operand skips the enclosing WHERE BOOL check while binding predicate children.
	operand bool
}
type expressionDependency struct {
	ref      ObjectRef
	revision DefinitionRevision
}
type expressionBinder struct {
	engine           *Engine
	ctx              context.Context
	relation         *relationDescriptor
	ref              ObjectRef
	parameters       map[int]sqltype.Usage
	updateParameters bool
	uses             []int
	dependencies     []expressionDependency
	input            sqltype.InputEnvironment
	op               *Operation
	expected         *StatementDescription
	temporals        []temporalConstant
	anyParameters    []parameterExpression
	deleteTarget     bool
	targetName       string
	targetAlias      string
	defaultConsumer  bool
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
	if len(raw.Name.Parts) == 0 || len(raw.Name.Parts) > 2 || (len(raw.Name.Parts) == 2 && !b.deleteTarget) {
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
	case parser.FunctionExpr:
		var err error
		node, err = b.bindFunction(r)
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
		if b.defaultConsumer || r.Number <= 0 {
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
		saved, found := b.captured(r.Pos.Offset)
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
		node, err = b.cast(r)
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
		if len(r.Name.Parts) == 1 && (arithmeticName(r.Name.Parts[0].Text) || symbolicArithmeticName(r.Name.Parts[0].Text)) {
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
	if policy.context == assignmentContext && node.ResultUsage() != policy.expected {
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
		node = coercionExpression{b.info(policy.expected, raw.SourcePosition(), node.Properties()), node, plan, policy.mode}
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
	d := expressionError(code, fmt.Sprintf("function %s(%s) %s", raw.Callee, source, ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *expressionBinder) cast(raw parser.CastExpr) (BoundExpr, error) {
	if raw.Syntax == parser.FunctionCast && raw.Callee != "int4" && raw.Callee != "int8" && raw.Callee != "text" && raw.Callee != "bool" {
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
	if !sqltype.BaselineCastType(target.Type.ID()) && !sqltype.IsCharacter(target.Type.ID()) {
		return nil, expressionError("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	policy := expressionPolicy{context: projectionContext}
	switch child := raw.Child.(type) {
	case parser.LiteralExpr:
		if child.Value.Kind == parser.StringValue || child.Value.Kind == parser.NullValue {
			policy.expected = target
		}
	case parser.ParameterExpr:
		if b.defaultConsumer {
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
	plan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
	if err != nil {
		if !sqltype.BaselineCastType(source.Type.ID()) || !sqltype.BaselineCastType(target.Type.ID()) {
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
	return coercionExpression{b.info(target, pos, child.Properties()), child, plan, sqltype.Explicit}, nil
}

// Strong column/function names survive standard casts; weak literal names do not.
func expressionName(raw parser.Expr, usage sqltype.Usage) (string, bool) {
	switch r := raw.(type) {
	case parser.ColumnExpr:
		return r.Name.Parts[0].Text, true
	case parser.FunctionExpr:
		return r.Name.Parts[0].Text, true
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
func (b *expressionBinder) captured(offset int) (temporalConstant, bool) {
	if b.expected != nil {
		captures := b.expected.temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].offset >= offset })
		if i < len(captures) && captures[i].offset == offset {
			return captures[i], true
		}
	}
	return temporalConstant{}, false
}
func (b *expressionBinder) inputConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time && u.Type.ID() != sqltype.Date {
		return u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
	}
	if c, ok := b.captured(pos.Offset); ok {
		if c.typeID != u.Type.ID() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: captured temporal type changed")
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
			v, err = p.Apply(b.ctx, v)
		}
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if b.expected == nil {
		var n int64
		var err error
		if u.Type.ID() == sqltype.Time {
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
		b.temporals[i] = temporalConstant{pos.Offset, n, u.Typmod, u.Type.ID()}
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
		copy.Message = fmt.Sprintf("operator does not exist: %s %s", raw.Name.Parts[0].Text, name(left))
		copy.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
	} else {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", name(left), raw.Name.Parts[0].Text, name(right))
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
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return comparisonOperand{raw: raw}, nil
	}
	switch raw.(type) {
	case parser.ColumnExpr, parser.ParameterExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.CastExpr, parser.OperatorExpr:
	default:
		return comparisonOperand{}, fmt.Errorf("stuntgres: invalid comparison operand")
	}
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	return comparisonOperand{node: node}, err
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

func (b *expressionBinder) bindComparison(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid comparison expression")
	}
	var operator sqltype.ComparisonOperator
	switch raw.Name.Parts[0].Text {
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
	if lt == nil {
		lt = rt
	}
	if rt == nil {
		rt = lt
	}
	if lt == nil {
		lt, rt = b.typ(sqltype.Text), b.typ(sqltype.Text)
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, lt, rt, b.typ(sqltype.Bool))
	if err != nil {
		return nil, operatorExpressionError(err, left.descriptor(), right.descriptor(), raw)
	}
	l, err := b.finalizeOperand(left, lt)
	if err != nil {
		return nil, err
	}
	r, err := b.finalizeOperand(right, rt)
	if err != nil {
		return nil, err
	}
	p, rp := l.Properties(), r.Properties()
	p.RowDependent = p.RowDependent || rp.RowDependent
	p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
	p.StageDependent = p.StageDependent || rp.StageDependent
	return comparisonExpression{b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, p), l, r, predicate, raw.Name.Parts[0].Text}, nil
}

type arithmeticExpression struct {
	expressionInfo
	left, right BoundExpr
	provider    sqltype.Arithmetic
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
	if len(raw.Name.Parts) != 1 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid arithmetic expression")
	}
	name := raw.Name.Parts[0].Text
	// Preserve signed NUMERIC literal input without admitting a NUMERIC provider.
	if name == "+" && len(raw.Args) == 1 {
		if literal, ok := raw.Args[0].(parser.LiteralExpr); ok && literal.Value.Kind == parser.NumericValue {
			literal.Value.Pos = raw.Pos
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
			n = coercionExpression{b.info(typ.Usage(), n.SourcePosition(), n.Properties()), n, plan}
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
	return arithmeticExpression{b.info(typ.Usage(), pos, p), l, r, provider}, nil
}
