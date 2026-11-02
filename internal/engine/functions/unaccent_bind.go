package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindInstalledText retains the genuine installed signature and analyzes each
// operand once. The explicit dictionary uses ordinary REGDICTIONARY coercion;
// the shared daitch_mokotoff path remains unary TEXT.
func bindInstalledText(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	daitch := strings.EqualFold(name, "daitch_mokotoff")
	binary := !daitch && len(raw.Args) == 2
	var small [2]Argument
	operands := small[:0]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		intent := StringTextArgument
		if binary && i == 0 {
			intent = ComparisonArgument
		}
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: intent})
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	extension, function, signature := UnaccentExtension, "unaccent", "25"
	result, volatility := sqltype.Text, expr.Stable
	if daitch {
		extension, function = FuzzystrmatchExtension, "daitch_mokotoff"
		result, volatility = sqltype.ID(1009), expr.Immutable
	} else if binary {
		signature = "3769,25"
	}
	routine, err := b.InstalledRoutine(raw.Name, extension, signature)
	if err != nil {
		return nil, err
	}
	failure := func() (expr.Expr, error) {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if routine == nil || len(operands) != 1 && !binary || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star || raw.Within {
		return failure()
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: volatility}
	for i, operand := range operands {
		formal := b.Type(sqltype.Text)
		if binary && i == 0 {
			formal = b.Type(sqltype.Regdictionary)
		}
		if operand.Usage.Type != nil && !b.CanCoerce(operand.Usage, formal.Usage(), sqltype.Implicit) {
			return failure()
		}
		arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: formal.Usage()})
		if err != nil {
			return nil, err
		}
		coercion, err := sqltype.PlanCoercion(arg.ResultUsage(), formal.Usage(), sqltype.Implicit)
		if err != nil {
			return failure()
		}
		if arg.ResultUsage().Type != formal {
			arg, err = b.CoerceArgument(Argument{Expression: arg, Usage: arg.ResultUsage()}, CoercionRequest{Intent: DirectFormalArgument, Target: formal.Usage(), Plan: coercion, Planned: true})
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.CollatedInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), args[:len(operands)]...)
	if err != nil {
		return nil, err
	}
	call := InstalledText{Info: info, Arg: args[0], Function: function}
	if binary {
		call.Dictionary, call.Arg = args[0], args[1]
	}
	return routine.Attach(call)
}
