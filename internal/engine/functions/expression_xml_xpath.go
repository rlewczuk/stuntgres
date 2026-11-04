package functions

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// XMLXPath retains written operands and overload identity, never invocation state.
type XMLXPath struct {
	expr.Info
	Args    []expr.Expr
	Name    string
	Wrapper bool
	result  *sqltype.Descriptor
	routine expr.RoutineReference
	formals [3]*sqltype.Descriptor
}

func (e XMLXPath) Eval(c expr.Context) (sqltype.Value, error) {
	var values [3]sqltype.Value
	null := false
	for i, child := range e.Args {
		value, err := child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	body, err := e.EnterRoutine(c, values[:len(e.Args)])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	path, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := sqltype.XMLXPath(ctx, e.Usage.Type, path, values[1], values[2], e.Name == "xpath_exists", c.FunctionInput(), c.FunctionOutput())
	if err != nil && e.Wrapper {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Context = "SQL function \"" + e.Name + "\" statement 1"
			return sqltype.Value{}, &copy
		}
	}
	return value, err
}
func (e XMLXPath) Children() []expr.Expr { return e.Args }
func (e XMLXPath) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if len(children) != len(e.Args) {
		return nil, fmt.Errorf("invalid XPath children")
	}
	e.Info, e.Args = info, children
	return e, e.ValidateStoredProvider()
}
func (e XMLXPath) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, false, err
	}
	e.result, e.routine = env.Type(e.result), info.RoutineReference
	for i := range len(e.Args) {
		e.formals[i] = env.Type(e.formals[i])
	}
	e.Info, e.Args = info, children
	node, err := e.Rebuild(info, children)
	return node, changed, err
}
func (XMLXPath) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (XMLXPath) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (XMLXPath) PreflightFold() expr.Preflight        { return expr.PreflightXPath }
func (XMLXPath) StrictNull() expr.Strict              { return expr.StrictAfter }
func (XMLXPath) LooksLikeFunction() bool              { return true }
func (XMLXPath) ClosureTransparent()                  {}
func (e XMLXPath) WithWrapperRoute(wrapper bool) XMLXPath {
	e.Wrapper = len(e.Args) == 2 && wrapper
	return e
}
func (e XMLXPath) ValidateStoredProvider() error {
	if len(e.Args) < 2 || len(e.Args) > 3 || e.Name != "xpath" && e.Name != "xpath_exists" || e.Usage.Type == nil || e.RoutineReference == nil {
		return fmt.Errorf("invalid XPath recipe")
	}
	if e.Usage.Type != e.result || e.RoutineReference != e.routine || e.Usage != e.Usage.Type.Usage() {
		return fmt.Errorf("invalid XPath generation")
	}
	for i, child := range e.Args {
		if child == nil || child.ResultUsage().Type != e.formals[i] {
			return fmt.Errorf("invalid XPath operand generation")
		}
	}
	expected := uint32(2932)
	result := sqltype.ID(143)
	if e.Name == "xpath_exists" {
		expected, result = 3050, sqltype.Bool
	}
	if len(e.Args) == 3 {
		expected--
	}
	if e.RoutineReference.RoutineOID() != expected || e.Usage.Type.ID() != result || !xmlPredicateOperand(e.Args[0], sqltype.Text) || !xmlPredicateOperand(e.Args[1], sqltype.XML) {
		return fmt.Errorf("invalid XPath signature")
	}
	if len(e.Args) == 3 && !xmlPredicateOperand(e.Args[2], 1009) {
		return fmt.Errorf("invalid XPath namespaces")
	}
	if result == 143 && (!e.Usage.Type.IsArray() || e.Usage.Type.Element().ID() != sqltype.XML) {
		return fmt.Errorf("invalid XPath result")
	}
	return nil
}
func (e XMLXPath) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: len(e.Args), Args: e.Args}, kind == expr.CaptureFunction, e.ValidateStoredProvider()
}
func (e XMLXPath) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(e.Args))
	for i, child := range e.Args {
		args[i] = expr.RenderArg{Expr: child}
	}
	return expr.Rendering{Tree: e.Name, Inputs: e.Args, Form: expr.FormCall, Name: e.Name, Args: args}, e.ValidateStoredProvider()
}
func (e XMLXPath) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(XMLXPath)
	if !ok || e.Name != o.Name || e.routine != o.routine || e.result != o.result || len(e.Args) != len(o.Args) {
		return false
	}
	for i, child := range e.Args {
		if !same(child, o.Args[i]) {
			return false
		}
	}
	return true
}
func bindXMLXPathCall(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if len(raw.Args) < 2 || len(raw.Args) > 3 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.CallSignatureError(raw)
	}
	var operands [3]Argument
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, b.CallSignatureError(raw)
	}
	formals := []sqltype.ID{sqltype.Text, sqltype.XML, 1009}[:len(raw.Args)]
	children := make([]expr.Expr, len(raw.Args))
	properties := expr.Properties{}
	for i, id := range formals {
		children[i], err = coerceInquiryFormal(b, operands[i], raw.Args[i], b.Type(id).Usage(), func() error { return b.CallSignatureError(raw) })
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, children[i].Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	result := sqltype.ID(143)
	if name == "xpath_exists" {
		result = sqltype.Bool
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals...)
	if err != nil {
		return nil, err
	}
	node := XMLXPath{Info: info, Args: children, Name: name, Wrapper: len(children) == 2 && !properties.RowDependent && !properties.StageDependent && !properties.DomainDependent}
	node.result, node.routine = info.Usage.Type, info.RoutineReference
	for i, child := range children {
		node.formals[i] = child.ResultUsage().Type
	}
	return node, node.ValidateStoredProvider()
}
