package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) BindUnnest(raw parser.FunctionExpr) (expr.Expr, error) {
	var small [1]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = arg
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	var operand functions.Argument
	if len(operands) == 1 {
		operand = operands[0]
	}
	input, result := plan.UnnestInputTypes(operand.Usage)
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !visible || name != "unnest" || len(raw.ArgNames) != 0 || len(raw.Args) != 1 || result == nil {
		names := make([]string, len(raw.Args))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = functions.ArrayTypeName(b, arg.Usage.Type)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
		ambiguous := visible && name == "unnest" && len(raw.ArgNames) == 0 && len(raw.Args) == 1 && operand.Usage.Type == nil
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, ambiguous)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	target := input.Usage()
	if input.IsArray() {
		target = operand.Usage.BaseUsage()
	}
	arg, err := b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: target, Label: "unnest"})
	if err != nil {
		return nil, err
	}
	if b.Params.ExecuteParameters {
		return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos)
	}
	if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	switch b.Clause.AggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.Clause.QueryHasTargetSet = true
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable, StageDependent: true}, arg.Properties())
	usage := result.Usage()
	if input.IsArray() {
		usage.Collation = target.Collation
	}
	info := b.Info(usage, raw.Pos, properties)
	if usage.Collation.ID != 0 {
		info, err = b.CollatedInfo(info, arg)
		if err != nil {
			return nil, err
		}
	}
	provider, formal := functions.RangeRoutineProvider, sqltype.ID(4537)
	if input.IsArray() {
		provider, formal = functions.ArrayRoutineProvider, sqltype.ID(2277)
	}
	info, err = b.builtinRoutineInfo(info, provider, "unnest", formal)
	if err != nil {
		return nil, err
	}
	return plan.UnnestExpr{Info: info, Args: []expr.Expr{arg}}, nil
}
