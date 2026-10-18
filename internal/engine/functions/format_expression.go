package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Format retains ANY operands in their original types: OUTPUT is not the
// implicit TEXT conversion used by strict string functions or concatenation.
type Format struct {
	expr.Info
	Args       []expr.Expr
	Variadic   bool
	Int4       *sqltype.Descriptor
	RawUnknown [2]uint64
}

func (e Format) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if len(e.Args) < 1 || len(e.Args) > 100 || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Text || e.Int4 == nil || e.Int4.ID() != sqltype.Int4 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid format expression")
	}
	var scratch [100]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != arg.ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid format argument type")
		}
		scratch[i] = value
	}
	// NONSTRICT does not suppress evaluation of NULL-format or unused children.
	if scratch[0].IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	pattern, err := scratch[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	values := FormatValues{Ordinary: scratch[1:len(e.Args)]}
	if e.Variadic {
		if len(e.Args) != 2 {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid variadic format arity")
		}
		values = FormatValues{Array: scratch[1]}
	}
	text, err := FormatText(ctx, pattern, values, e.Int4, c.FunctionFormatOutput())
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(e.Usage.Type, text), nil
}
func (e Format) Validate() error {
	if len(e.Args) < 1 || len(e.Args) > 100 || e.Usage.Type.ID() != sqltype.Text || e.Args[0].ResultUsage().Type.ID() != sqltype.Text || e.Int4 == nil || e.Int4.ID() != sqltype.Int4 || e.Variadic && (len(e.Args) != 2 || !e.Args[1].ResultUsage().Type.BaseType().IsArray()) {
		return fmt.Errorf("stuntgres: invalid stored format signature")
	}
	return nil
}
func FormatArgumentAnalysis(index int) AnalysisRequest {
	if index == 0 {
		return AnalysisRequest{Intent: StringTextArgument}
	}
	return AnalysisRequest{Intent: ProjectionArgument}
}
func FormatUnknownArgument(raw parser.Expr, index int) bool {
	literal, ok := raw.(parser.LiteralExpr)
	return index > 0 && index < 100 && ok && literal.Value.Kind == parser.StringValue
}
func ResolveFormat(visible, variadic bool, count int, first sqltype.Usage) Resolution {
	valid := visible && count > 0 && (!variadic || count == 2)
	if count > 0 && first.Type != nil {
		valid = valid && sqltype.StringTextInput(first.Type.BaseType().ID())
	}
	if valid {
		return Resolved
	}
	return Missing
}
func CoerceFormatFirst(b ArgumentCoercer, first Argument, text sqltype.Usage) (expr.Expr, error) {
	if first.Usage.Type == nil {
		return b.CoerceArgument(first, CoercionRequest{Intent: FinalizeArgument, Target: text})
	}
	return first.Expression, nil
}
func FormatFirstCoercion(source, text sqltype.Usage) (CoercionRequest, bool) {
	if source.Type == text.Type {
		return CoercionRequest{}, false
	}
	target := text
	if source.Collation.ID != 0 {
		target.Collation = source.Collation
	}
	return CoercionRequest{Intent: TextFormalArgument, Target: target}, true
}
func FormatIntrinsicRule() expr.IntrinsicPropertyRule { return expr.ExactStable }

// Format implements expr.Composite. It has no provider to revalidate, so Rebuild
// only takes the new metadata and children.
var _ expr.Composite = Format{}

func (e Format) Children() []expr.Expr { return e.Args }
func (e Format) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Format", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, append([]expr.Expr(nil), children...)
	return e, nil
}

func (e Format) IntrinsicVolatility() expr.Volatility { return expr.Stable }

// PlanningFold: format is intrinsically STABLE, even with literal children.
func (e Format) PlanningFold() expr.Fold { return expr.FoldNever }

func (e Format) ValidateStoredProvider() error { return nil }

// Retag retains the INT4 descriptor generation.
func (e Format) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	int4 := env.Type(e.Int4)
	changed = changed || int4 != e.Int4
	e.Info, e.Args, e.Int4 = info, children, int4
	return e, changed, nil
}

var _ expr.Renderable = Format{}

// Render implements expr.Renderable.
func (e Format) Render() (expr.Rendering, error) {
	spec := variadicRendering(fmt.Sprintf("Format variadic %t", e.Variadic), "format", e.Args, e.Variadic)
	spec.UnknownTexts = true
	for i := range spec.Args {
		spec.Args[i].UnknownText = e.RawUnknown[i/64]&(uint64(1)<<uint(i%64)) != 0
	}
	return spec, nil
}
