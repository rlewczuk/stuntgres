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
	ConvertJSONTextSearch(bool, sqltype.Value, sqltype.Value, sqltype.Value, bool, *sqltype.Descriptor) (sqltype.Value, error)
}

// FTSConversion owns checked SQL operands, never configuration or dictionary state.
type FTSConversion struct {
	expr.Info
	Args     [3]expr.Expr
	Kind     fts.ConversionKind
	Explicit bool
	Input    sqltype.ID
	Filtered bool
}

func bindFTSConversion(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [3]Argument
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	filtered := name == "json_to_tsvector" || name == "jsonb_to_tsvector"
	explicit := len(operands) == 2
	input := sqltype.Text
	if filtered {
		explicit = len(operands) == 3
		input = sqltype.JSON
		if name == "jsonb_to_tsvector" {
			input = sqltype.JSONB
		}
	} else if kind == fts.VectorConversion && len(operands) > 0 {
		last := operands[len(operands)-1].Usage.Type
		if last != nil && (last.BaseType().ID() == sqltype.JSON || last.BaseType().ID() == sqltype.JSONB) {
			input = last.BaseType().ID()
		}
	}
	var formalStorage [3]sqltype.ID
	formals := formalStorage[:0]
	if explicit {
		formals = append(formals, sqltype.Regconfig)
	}
	formals = append(formals, input)
	if filtered {
		formals = append(formals, sqltype.JSONB)
	}
	valid := visible && len(operands) == len(formals) && len(raw.ArgNames) == 0 && !raw.Variadic
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
	var args [3]expr.Expr
	volatility := expr.Stable
	if explicit {
		volatility = expr.Immutable
	}
	properties := expr.Properties{Volatility: volatility}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	result := sqltype.TSQuery
	if kind == fts.VectorConversion {
		result = sqltype.TSVector
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), FTSQueryRoutineProvider, name, formals...)
	if err != nil {
		return nil, err
	}
	e := FTSConversion{Info: info, Args: args, Kind: kind, Explicit: explicit, Input: input, Filtered: filtered}
	return e, e.Validate()
}
func (e FTSConversion) arity() int {
	n := 1
	if e.Explicit {
		n++
	}
	if e.Filtered {
		n++
	}
	return n
}
func (e FTSConversion) inputType() sqltype.ID {
	return e.Input
}
func (e FTSConversion) name() string {
	if e.Filtered {
		if e.inputType() == sqltype.JSONB {
			return "jsonb_to_tsvector"
		}
		return "json_to_tsvector"
	}
	return e.Kind.Name()
}
func (e FTSConversion) Validate() error {
	result := sqltype.TSQuery
	if e.Kind == fts.VectorConversion {
		result = sqltype.TSVector
	}
	if !e.Kind.Valid() || e.Usage.Type == nil || e.Usage.Type.ID() != result {
		return fmt.Errorf("invalid text search conversion result")
	}
	if e.inputType() != sqltype.Text && e.inputType() != sqltype.JSON && e.inputType() != sqltype.JSONB ||
		(e.Filtered || e.inputType() != sqltype.Text) && e.Kind != fts.VectorConversion ||
		e.Filtered && e.inputType() == sqltype.Text {
		return fmt.Errorf("invalid text search conversion input kind")
	}
	for i, arg := range e.Children() {
		id := e.inputType()
		if e.Filtered && i == e.arity()-1 {
			id = sqltype.JSONB
		}
		if e.Explicit && i == 0 {
			id = sqltype.Regconfig
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("invalid text search conversion operand")
		}
	}
	for _, arg := range e.Args[e.arity():] {
		if arg != nil {
			return fmt.Errorf("unexpected text search conversion operand")
		}
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
	var values [3]sqltype.Value
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
	runtime, ok := c.(FTSConversionRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: text search conversion lacks statement authority")
	}
	var config sqltype.Value
	if e.Explicit {
		config = values[0]
	}
	if e.inputType() != sqltype.Text {
		documentIndex := 0
		if e.Explicit {
			documentIndex++
		}
		var filter sqltype.Value
		if e.Filtered {
			filter = values[e.arity()-1]
		}
		return runtime.ConvertJSONTextSearch(e.Explicit, config, values[documentIndex], filter, e.Filtered, e.Usage.Type)
	}
	text, err := values[e.arity()-1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.ConvertTextSearch(e.Kind, e.Explicit, config, text, e.Usage.Type)
}
func (e FTSConversion) Children() []expr.Expr { return e.Args[:e.arity()] }
func (e FTSConversion) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSConversion", children, e.arity()); err != nil {
		return nil, err
	}
	next := FTSConversion{Info: info, Kind: e.Kind, Explicit: e.Explicit, Input: e.Input, Filtered: e.Filtered}
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
	spec := callRendering("text search conversion", e.Children(), e.name(), e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e FTSConversion) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSConversion)
	if !ok || e.Kind != o.Kind || e.Explicit != o.Explicit || e.inputType() != o.inputType() || e.Filtered != o.Filtered {
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
