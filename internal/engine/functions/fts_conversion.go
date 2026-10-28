package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type FTSConversionRuntime interface {
	ConvertTextSearch(fts.ConversionKind, bool, sqltype.Value, string, *sqltype.Descriptor) (sqltype.Value, error)
}

// FTSConversion owns checked SQL operands, never configuration or dictionary state.
type FTSConversion struct {
	expr.Info
	Args     [2]expr.Expr
	Kind     fts.ConversionKind
	Explicit bool
}

func bindFTSConversion(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := fts.VectorConversion
	for _, k := range []fts.ConversionKind{fts.VectorConversion, fts.StrictConversion, fts.PlainConversion, fts.PhraseConversion, fts.WebConversion} {
		if raw.Name.Parts[len(raw.Name.Parts)-1].Text == k.Name() {
			kind = k
			break
		}
	}
	explicit := len(operands) == 2
	formals := []sqltype.ID{sqltype.Text}
	if explicit {
		formals = []sqltype.ID{sqltype.Regconfig, sqltype.Text}
	}
	valid := visible && (len(operands) == 1 || explicit) && len(raw.ArgNames) == 0 && !raw.Variadic
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
	volatility := expr.Stable
	if explicit {
		volatility = expr.Immutable
	}
	properties := expr.Properties{Volatility: volatility}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: kind.Name()})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	result := sqltype.TSQuery
	if kind == fts.VectorConversion {
		result = sqltype.TSVector
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), FTSQueryRoutineProvider, kind.Name(), formals...)
	if err != nil {
		return nil, err
	}
	e := FTSConversion{Info: info, Args: args, Kind: kind, Explicit: explicit}
	return e, e.Validate()
}
func (e FTSConversion) arity() int {
	if e.Explicit {
		return 2
	}
	return 1
}
func (e FTSConversion) Validate() error {
	result := sqltype.TSQuery
	if e.Kind == fts.VectorConversion {
		result = sqltype.TSVector
	}
	if !e.Kind.Valid() || e.Usage.Type == nil || e.Usage.Type.ID() != result {
		return fmt.Errorf("invalid text search conversion result")
	}
	for i, arg := range e.Children() {
		id := sqltype.Text
		if e.Explicit && i == 0 {
			id = sqltype.Regconfig
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("invalid text search conversion operand")
		}
	}
	if !e.Explicit && e.Args[1] != nil {
		return fmt.Errorf("unexpected text search conversion operand")
	}
	return nil
}
func (e FTSConversion) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Children() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Children() {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for _, v := range values[:e.arity()] {
		if v.IsNull() {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	text, err := values[e.arity()-1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := c.(FTSConversionRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: text search conversion lacks statement authority")
	}
	var config sqltype.Value
	if e.Explicit {
		config = values[0]
	}
	return runtime.ConvertTextSearch(e.Kind, e.Explicit, config, text, e.Usage.Type)
}
func (e FTSConversion) Children() []expr.Expr { return e.Args[:e.arity()] }
func (e FTSConversion) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSConversion", children, e.arity()); err != nil {
		return nil, err
	}
	next := FTSConversion{Info: info, Kind: e.Kind, Explicit: e.Explicit}
	copy(next.Args[:], children)
	return next, next.Validate()
}
func (e FTSConversion) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e FTSConversion) ValidateStoredProvider() error { return e.Validate() }
func (e FTSConversion) IntrinsicVolatility() expr.Volatility {
	if e.Explicit {
		return expr.Immutable
	}
	return expr.Stable
}
func (FTSConversion) PreflightFold() expr.Preflight { return expr.PreflightNullOrEvaluate }
func (FTSConversion) LooksLikeFunction() bool       { return true }
func (FTSConversion) ClosureTransparent()           {}
func (e FTSConversion) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("text search conversion", e.Children(), e.Kind.Name(), e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e FTSConversion) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSConversion)
	if !ok || e.Kind != o.Kind || e.Explicit != o.Explicit {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e FTSConversion) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.arity(), Indexed: true}, true, nil
}
func (e FTSConversion) CaptureOperand(i int) (expr.Expr, error) {
	if i < 0 || i >= e.arity() {
		return nil, fmt.Errorf("invalid text search conversion operand")
	}
	return e.Args[i], nil
}

var _ expr.Composite = FTSConversion{}
var _ expr.Renderable = FTSConversion{}
