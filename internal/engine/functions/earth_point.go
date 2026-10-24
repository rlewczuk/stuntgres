package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindEarthPoint(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var storage [2]Argument
	operands := storage[:min(len(raw.Args), len(storage))]
	if len(raw.Args) > len(storage) {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	selected, err := b.InstalledRoutine(raw.Name, EarthdistanceExtension, "600,600")
	if err != nil {
		return nil, err
	}
	if selected == nil || len(operands) != 2 || raw.Variadic || raw.Star || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := selected.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	target := b.Type(sqltype.Point).Usage()
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		if o.Usage.Type != nil && !b.CanCoerce(o.Usage, target, sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
		}
		args[i], err = b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: "geo_distance"})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	call, err := NewEarthPoint(b.Info(b.Type(sqltype.Float8).Usage(), raw.Pos, properties), args)
	if err != nil {
		return nil, err
	}
	return selected.Attach(call)
}

// EarthPoint owns only builtin POINT/FLOAT8 generations. Installed authority
// belongs to its plan wrapper, including calls with literal NULL operands.
type EarthPoint struct {
	expr.Info
	Plan sqltype.EarthPointPlan
	Args [2]expr.Expr
}

func NewEarthPoint(info expr.Info, args [2]expr.Expr) (EarthPoint, error) {
	if missingArgument(args[:]) {
		return EarthPoint{}, fmt.Errorf("stuntgres: missing earth point operand")
	}
	p, err := sqltype.BindEarthPoint(info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return EarthPoint{Info: info, Plan: p, Args: args}, err
}
func (e EarthPoint) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e EarthPoint) Children() []expr.Expr { return e.Args[:] }
func (e EarthPoint) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("EarthPoint", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(NewEarthPoint(info, [2]expr.Expr{children[0], children[1]}))
}
func (e EarthPoint) Validate() error {
	n, err := NewEarthPoint(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored earth point kernel")
	}
	return nil
}
func (e EarthPoint) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e EarthPoint) ValidateStoredProvider() error        { return e.Validate() }
func (e EarthPoint) Render() (expr.Rendering, error) {
	spec := callRendering("EarthPoint geo_distance", e.Children(), "geo_distance", e.Children())
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e EarthPoint) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(EarthPoint)
	return ok && e.Plan == o.Plan && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e EarthPoint) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e EarthPoint) ClosureTransparent() {}
func (e EarthPoint) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}

var _ expr.Composite = EarthPoint{}
var _ expr.Renderable = EarthPoint{}
