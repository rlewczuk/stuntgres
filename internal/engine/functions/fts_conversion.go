package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type FTSConversionRuntime interface {
	ConvertTSVector(sqltype.Value, string, *sqltype.Descriptor) (sqltype.Value, error)
}
type ToTSVector struct {
	expr.Info
	Args [2]expr.Expr
}

func bindToTSVector(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]Argument
	operands := small[:0]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	formals := [2]sqltype.ID{sqltype.Regconfig, sqltype.Text}
	valid := visible && len(operands) == 2 && len(raw.ArgNames) == 0 && !raw.Variadic
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(formals[i]).Usage(), sqltype.Implicit); err != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, ResolutionError(raw.Name, raw.Pos, names, false)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: "to_tsvector"})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.TSVector).Usage(), raw.Pos, properties), FTSQueryRoutineProvider, "to_tsvector", formals[:]...)
	if err != nil {
		return nil, err
	}
	e := ToTSVector{info, args}
	return e, e.Validate()
}
func (e ToTSVector) Validate() error {
	if e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.TSVector {
		return fmt.Errorf("invalid to_tsvector result")
	}
	for i, id := range [2]sqltype.ID{sqltype.Regconfig, sqltype.Text} {
		if e.Args[i] == nil || e.Args[i].ResultUsage().Type == nil || e.Args[i].ResultUsage().Type.ID() != id {
			return fmt.Errorf("invalid to_tsvector operand")
		}
	}
	return nil
}
func (e ToTSVector) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	text, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := c.(FTSConversionRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: to_tsvector lacks statement authority")
	}
	return runtime.ConvertTSVector(values[0], text, e.Usage.Type)
}
func (e ToTSVector) Children() []expr.Expr { return e.Args[:] }
func (e ToTSVector) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ToTSVector", children, 2); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	copy(args[:], children)
	next := ToTSVector{info, args}
	return next, next.Validate()
}
func (e ToTSVector) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e ToTSVector) ValidateStoredProvider() error      { return e.Validate() }
func (ToTSVector) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (ToTSVector) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (ToTSVector) LooksLikeFunction() bool              { return true }
func (ToTSVector) ClosureTransparent()                  {}
func (e ToTSVector) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("text search conversion", e.Children(), "to_tsvector", e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e ToTSVector) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(ToTSVector)
	return ok && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e ToTSVector) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 2, Indexed: true}, true, nil
}
func (e ToTSVector) CaptureOperand(i int) (expr.Expr, error) {
	if i < 0 || i >= 2 {
		return nil, fmt.Errorf("invalid to_tsvector operand")
	}
	return e.Args[i], nil
}

var _ expr.Composite = ToTSVector{}
var _ expr.Renderable = ToTSVector{}
