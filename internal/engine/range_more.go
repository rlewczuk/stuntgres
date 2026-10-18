package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type unnestExpression struct {
	expressionInfo
	args      []BoundExpr
	procedure *procedureDescriptor
	ref       ids.ObjectRef
}

func (e unnestExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted unnest call")
}
func (e unnestExpression) mapChildren(mapper func(BoundExpr) (BoundExpr, bool, error)) (unnestExpression, bool, error) {
	arg, changed, err := mapper(e.args[0])
	if err != nil {
		return e, false, err
	}
	if changed {
		e.args = []BoundExpr{arg}
	}
	return e, changed, nil
}
func (b *expressionBinder) bindUnnest(raw parser.FunctionExpr) (BoundExpr, error) {
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
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	var operand functions.Argument
	if len(operands) == 1 {
		operand = operands[0]
	}
	var input, result *sqltype.Descriptor
	if len(raw.Args) == 1 && operand.Usage.Type != nil {
		input = operand.Usage.Type.BaseType()
		if input.IsArray() {
			result = input.Element()
		} else {
			result = sqltype.MultirangeUnnestResult(input)
		}
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !visible || name != "unnest" || len(raw.ArgNames) != 0 || len(raw.Args) != 1 || result == nil {
		names := make([]string, len(raw.Args))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.arraySearchTypeName(arg.Usage.Type)
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
	if b.executeParameters {
		return nil, expressionError("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos)
	}
	if b.policyConsumer && b.queryFrame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
	}
	properties := mergeExpressionProperties(ExprProperties{Volatility: Immutable, StageDependent: true}, arg.Properties())
	usage := result.Usage()
	if input.IsArray() {
		usage.Collation = target.Collation
	}
	info := b.info(usage, raw.Pos, properties)
	if usage.Collation.ID != 0 {
		info, err = b.collatedInfo(info, arg)
		if err != nil {
			return nil, err
		}
	}
	provider, formal := rangeRoutineProvider, sqltype.ID(4537)
	if input.IsArray() {
		provider, formal = arrayRoutineProvider, sqltype.ID(2277)
	}
	info, err = b.builtinRoutineInfo(info, provider, "unnest", formal)
	if err != nil {
		return nil, err
	}
	return unnestExpression{expressionInfo: info, args: []BoundExpr{arg}}, nil
}
