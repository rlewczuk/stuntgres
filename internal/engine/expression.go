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
}

func (e coercionExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	v, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.plan.Apply(c.Context, v)
}

type equalityExpression struct {
	expressionInfo
	left, right BoundExpr
	equality    sqltype.Equality
}

func (e equalityExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.equality.Evaluate(c.Context, a, b)
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
	context  expressionContext
	expected sqltype.Usage
	mode     sqltype.CoercionContext
}
type expressionDependency struct {
	ref      ObjectRef
	revision DefinitionRevision
}
type expressionBinder struct {
	engine       *Engine
	ctx          context.Context
	relation     *relationDescriptor
	ref          ObjectRef
	parameters   map[int]sqltype.Usage
	uses         []int
	dependencies []expressionDependency
	input        sqltype.InputEnvironment
	op           *Operation
	expected     *StatementDescription
	temporals    []temporalConstant
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
		return sqlError("0A000", "parameter reused with different destination types")
	}
	b.parameters[n] = u.Type.Usage()
	return nil
}
func (b *expressionBinder) finish() (ParameterInfo, error) {
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
	text := b.typ(sqltype.Text).Usage()
	if existing := b.parameters[p.number]; existing.Type != nil && existing.Type != text.Type {
		d := expressionError("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.number), p.pos).(*sqlstate.Error)
		name := existing.Type.DiagnosticName()
		d.Detail = fmt.Sprintf("%s versus text", name)
		return nil, d
	}
	if err := b.constrain(p.number, text, p.pos); err != nil {
		return nil, err
	}
	return parameterExpression{b.info(text, p.pos, p.properties), p.number}, nil
}
func (b *expressionBinder) column(raw parser.ColumnExpr) (BoundExpr, error) {
	if b.relation == nil || len(raw.Name.Parts) != 1 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	name := raw.Name.Parts[0].Text
	i, ok := b.relation.index[name]
	if !ok {
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
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("stuntgres: missing scalar expression")
	}
	var node BoundExpr
	switch r := raw.(type) {
	case parser.ColumnExpr:
		if policy.context == assignmentContext {
			return nil, fmt.Errorf("stuntgres: column in assignment scalar")
		}
		var err error
		node, err = b.column(r)
		if err != nil {
			return nil, err
		}
	case parser.ParameterExpr:
		if r.Number <= 0 {
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
	case parser.OperatorExpr:
		if policy.context != filterContext || len(r.Args) != 2 {
			return nil, fmt.Errorf("stuntgres: invalid equality expression")
		}
		left, err := b.bind(r.Args[0], expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		right, err := b.bind(r.Args[1], expressionPolicy{context: projectionContext, expected: left.ResultUsage(), mode: sqltype.Implicit})
		if err != nil {
			return nil, operatorExpressionError(err, left.ResultUsage(), r)
		}
		equality, err := sqltype.BindEquality(left.ResultUsage().Type, right.ResultUsage().Type, b.typ(sqltype.Bool))
		if err != nil {
			return nil, operatorExpressionError(err, left.ResultUsage(), r)
		}
		p := left.Properties()
		p.ParameterDependent = p.ParameterDependent || right.Properties().ParameterDependent
		node = equalityExpression{b.info(b.typ(sqltype.Bool).Usage(), r.Pos, p), left, right, equality}
	default:
		return nil, fmt.Errorf("stuntgres: unsupported scalar node")
	}
	if policy.context == filterContext && node.ResultUsage().Type.ID() != sqltype.Bool {
		return nil, expressionError("42804", fmt.Sprintf("argument of WHERE must be type boolean, not type %s", node.ResultUsage().Type.DiagnosticName()), raw.SourcePosition())
	}
	if policy.context == assignmentContext && node.ResultUsage() != policy.expected {
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), policy.expected, policy.mode)
		if err != nil {
			return nil, err
		}
		node = coercionExpression{b.info(policy.expected, raw.SourcePosition(), node.Properties()), node, plan}
	}
	return node, nil
}
func inputErrorAt(err error, pos parser.Position) error {
	if d, ok := err.(*sqlstate.Error); ok && (d.Code == "22P02" || d.Code == "22003" || d.Code == "22007" || d.Code == "22008" || d.Code == "22009" || d.Code == "22023") {
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
	if u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time {
		return u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
	}
	if c, ok := b.captured(pos.Offset); ok {
		if c.typeID != u.Type.ID() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: captured temporal type changed")
		}
		if c.typeID == sqltype.Time {
			return sqltype.TimeValue(u.Type, c.scalar)
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
		} else {
			n, err = v.Timestamp()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		// The binder visits constant occurrences in source order.
		b.temporals = append(b.temporals, temporalConstant{pos.Offset, n, u.Typmod, u.Type.ID()})
	}
	return v, nil
}
func operatorExpressionError(err error, left sqltype.Usage, raw parser.OperatorExpr) error {
	d, ok := err.(*sqlstate.Error)
	if !ok {
		return err
	}
	copy := *d
	if copy.Code == "42883" {
		right := "unknown"
		if literal, ok := raw.Args[1].(parser.LiteralExpr); ok {
			right = literalTypeName(literal.Value)
		}
		if literal, ok := raw.Args[1].(parser.TypedLiteralExpr); ok {
			right = literal.TypeName.Name + " without time zone"
		}
		copy.Message = fmt.Sprintf("operator does not exist: %s = %s", left.Type.DiagnosticName(), right)
		copy.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		copy.Position = strconv.Itoa(raw.Pos.Character)
	}
	return &copy
}
