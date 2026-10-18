package engine

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindInstalledText binds unaccent(text) and fuzzystrmatch's
// daitch_mokotoff(text). Operands are transformed once before namespace and
// signature resolution, retaining actual diagnostic types for unknown and
// wrong-signature inputs. unaccent rejects decorations as a missing
// signature; daitch_mokotoff reports them after coercion, like the other
// fuzzystrmatch calls.
func bindInstalledText(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]functions.Argument, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringTextArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	names := installedArgumentNames(b, raw, operands)
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	daitch := strings.EqualFold(name, "daitch_mokotoff")
	extension, function := functions.UnaccentExtension, "unaccent"
	result, volatility := sqltype.Text, expr.Stable
	if daitch {
		extension, function = functions.FuzzystrmatchExtension, "daitch_mokotoff"
		result, volatility = sqltype.ID(1009), expr.Immutable
	}
	routine, err := b.InstalledRoutine(raw.Name, extension, "25")
	if err != nil {
		return nil, err
	}
	failure := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	if routine == nil || len(operands) != 1 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star {
		return failure()
	}
	if !daitch && (raw.Distinct || raw.All || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0) {
		return failure()
	}
	text := b.Type(sqltype.Text)
	arg, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: text.Usage()})
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.PlanCoercion(arg.ResultUsage(), text.Usage(), sqltype.Implicit)
	if err != nil {
		return failure()
	}
	if daitch {
		if err := functions.CallDecorationError(raw); err != nil {
			return nil, err
		}
	}
	if arg.ResultUsage().Type != text {
		// No implicit coercion to TEXT has a cast routine, so the coercion
		// node carries no routine identity.
		arg, err = b.CoerceArgument(functions.Argument{Expression: arg, Usage: arg.ResultUsage()}, functions.CoercionRequest{Intent: functions.DirectFormalArgument, Target: text.Usage(), Plan: plan, Planned: true})
		if err != nil {
			return nil, err
		}
	}
	properties := arg.Properties()
	properties.Volatility = max(properties.Volatility, volatility)
	info, err := b.CollatedInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), arg)
	if err != nil {
		return nil, err
	}
	return routine.Attach(functions.InstalledText{Info: info, Arg: arg, Function: function})
}
