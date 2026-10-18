package functions

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func StringVariadicFunctionName(name string) bool {
	switch strings.ToLower(name) {
	case "concat", "concat_ws", "quote_ident", "quote_literal", "quote_nullable", "pg_client_encoding":
		return true
	}
	return false
}

// Stable is the original intrinsic operation fact, independent of merged P.
// Origin and Frame are opaque owner occurrence facts, never interpreted here.
type StringVariadic struct {
	expr.Info
	Name        string
	Args        []expr.Expr
	Variadic    bool
	AnyElement  bool
	Cast        sqltype.CoercionPlan
	CastPlanned bool
	Stable      bool
	Frame       uint32
	Origin      any
}

func (e StringVariadic) IntrinsicStable() bool { return e.Stable }
func (e StringVariadic) WithOrigin(origin any) StringVariadic {
	e.Origin = origin
	return e
}
func (e StringVariadic) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var scratch [100]sqltype.Value
	if len(e.Args) > len(scratch) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid string variadic arity")
	}
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		scratch[i] = value
	}
	output := c.FunctionStringOutput()
	if e.Name == "pg_client_encoding" {
		return sqltype.String(e.Usage.Type, output.ClientEncoding), nil
	}
	var out strings.Builder
	if e.Name == "concat" || e.Name == "concat_ws" {
		start, separator := 0, ""
		if e.Name == "concat_ws" {
			if scratch[0].IsNull() {
				return sqltype.Null(e.Usage.Type), nil
			}
			var err error
			separator, err = scratch[0].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
			start = 1
		}
		values := FormatValues{Ordinary: scratch[start:len(e.Args)]}
		if e.Variadic {
			if scratch[start].IsNull() {
				return sqltype.Null(e.Usage.Type), nil
			}
			values = FormatValues{Array: scratch[start]}
		}
		emitted := false
		for i := range values.Len() {
			if err := ctx.Err(); err != nil {
				return sqltype.Value{}, err
			}
			value := values.At(i)
			if value.IsNull() {
				continue
			}
			text, err := value.Type().OutputWithEnvironment(ctx, value, output)
			if err != nil {
				return sqltype.Value{}, err
			}
			if emitted {
				if err := formatWrite(ctx, &out, separator); err != nil {
					return sqltype.Value{}, err
				}
			}
			if err := formatWrite(ctx, &out, text); err != nil {
				return sqltype.Value{}, err
			}
			emitted = true
		}
	} else {
		value := scratch[0]
		if value.IsNull() && e.Name != "quote_nullable" {
			return sqltype.Null(e.Usage.Type), nil
		}
		if e.AnyElement && !value.IsNull() {
			if e.CastPlanned {
				var err error
				value, err = e.Cast.Apply(ctx, value, c.FunctionInput(), output)
				if err != nil {
					return sqltype.Value{}, err
				}
			} else {
				text, err := value.Type().OutputWithEnvironment(ctx, value, output)
				if err != nil {
					return sqltype.Value{}, err
				}
				value = sqltype.String(e.Usage.Type, text)
			}
		}
		verb := rune('L')
		if e.Name == "quote_ident" {
			verb = 'I'
		}
		if err := formatField(ctx, &out, value, verb, 0, false, output); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(e.Usage.Type, out.String()), nil
}
func StringVariadicRoles(name string) (concatenation, textFirst bool) {
	return name == "concat" || name == "concat_ws", name == "quote_ident" || name == "quote_literal" || name == "quote_nullable" || name == "concat_ws"
}
func StringVariadicAnalysis(name string, index int) AnalysisRequest {
	_, textFirst := StringVariadicRoles(name)
	if index == 0 && textFirst {
		return AnalysisRequest{Intent: StringTextArgument}
	}
	return AnalysisRequest{Intent: StringProjectionArgument}
}
func ResolveStringVariadic(name string, visible, variadic, resolutionOnly bool, labels, count int, first sqltype.Usage) Resolution {
	valid := visible && labels == 0 && name == strings.ToLower(name) && !resolutionOnly
	switch name {
	case "concat":
		valid = valid && count >= 1 && (!variadic || count == 1)
	case "concat_ws":
		valid = valid && count >= 2 && (!variadic || count == 2)
	case "pg_client_encoding":
		valid = valid && count == 0
	default:
		valid = valid && count == 1
	}
	_, textFirst := StringVariadicRoles(name)
	if textFirst && count > 0 && first.Type != nil && (name == "quote_ident" || name == "concat_ws") {
		valid = valid && sqltype.StringTextInput(first.Type.BaseType().ID())
	}
	if valid {
		return Resolved
	}
	return Missing
}
func StringVariadicCharAmbiguous(name string, count int, first sqltype.Usage) bool {
	concatenation, _ := StringVariadicRoles(name)
	return !concatenation && name != "quote_ident" && count == 1 && first.Type != nil && first.Type.BaseType().ID() == sqltype.Char
}
func StringVariadicIntrinsicStable(name string) bool {
	return name == "concat" || name == "concat_ws" || name == "pg_client_encoding"
}

type StringVariadicFirst struct {
	AnyElement  bool
	Cast        sqltype.CoercionPlan
	CastPlanned bool
	Stable      bool
	CoerceText  bool
	Target      sqltype.Usage
}

func ResolveStringVariadicFirst(name string, source, text sqltype.Usage) StringVariadicFirst {
	selection := StringVariadicFirst{Stable: StringVariadicIntrinsicStable(name)}
	if !sqltype.StringTextInput(source.Type.BaseType().ID()) && name != "concat_ws" && name != "quote_ident" {
		selection.AnyElement = true
		if source.Type.BaseType().ID() != sqltype.Cidr {
			var err error
			selection.Cast, err = sqltype.PlanCoercion(source, text, sqltype.Explicit)
			selection.CastPlanned = err == nil
		}
		base := source.Type.BaseType()
		if base.IsEnum() || base.IsArray() {
			selection.Stable = true
		} else if base.ID() != sqltype.Float4 && base.ID() != sqltype.Float8 && (selection.CastPlanned && selection.Cast.SessionDependent() || !selection.CastPlanned && base.OutputSessionDependent()) {
			selection.Stable = true
		}
	} else if source.Type != text.Type {
		selection.CoerceText, selection.Target = true, text
		if source.Collation.ID != 0 {
			selection.Target.Collation = source.Collation
		}
	}
	return selection
}
func CoerceStringVariadicFirst(b ArgumentCoercer, first Argument, text sqltype.Usage) (expr.Expr, error) {
	if first.Usage.Type == nil {
		return b.CoerceArgument(first, CoercionRequest{Intent: FinalizeArgument, Target: text})
	}
	return first.Expression, nil
}
func StringVariadicFirstCoercion(selection StringVariadicFirst) CoercionRequest {
	return CoercionRequest{Intent: CollatedTextFormalArgument, Target: selection.Target}
}
func StringVariadicArray(usage sqltype.Usage) bool {
	return usage.Type != nil && usage.Type.BaseType().IsArray()
}
func StringVariadicResult(name string) sqltype.ID {
	if name == "pg_client_encoding" {
		return sqltype.Name
	}
	return sqltype.Text
}

func (e StringVariadic) Reconstruct(info expr.Info, args []expr.Expr) (StringVariadic, error) {
	if e.CastPlanned {
		plan, err := sqltype.PlanCoercion(args[0].ResultUsage(), info.Usage, sqltype.Explicit)
		if err != nil {
			return e, err
		}
		e.Cast = plan
	}
	e.Info, e.Args = info, args
	return e, nil
}

// StringVariadic implements expr.Composite.
var _ expr.Composite = StringVariadic{}

func (e StringVariadic) Children() []expr.Expr { return e.Args }
func (e StringVariadic) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("StringVariadic", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}

func (e StringVariadic) IntrinsicVolatility() expr.Volatility { return stableIf(e.IntrinsicStable()) }

func (e StringVariadic) PlanningFold() expr.Fold {
	if e.IntrinsicStable() {
		return expr.FoldNever
	}
	return expr.FoldQuoteCapture
}
