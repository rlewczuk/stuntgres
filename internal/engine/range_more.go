package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type multirangeUnnestExpression struct {
	expressionInfo
	args []BoundExpr
}

func (e multirangeUnnestExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil { return sqltype.Value{}, err }
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted multirange unnest call")
}

func (e multirangeUnnestExpression) mapChildren(mapper func(BoundExpr) (BoundExpr, bool, error)) (multirangeUnnestExpression, bool, error) {
	arg, changed, err := mapper(e.args[0])
	if err != nil { return e, false, err }
	if changed { e.args = []BoundExpr{arg} }
	return e, changed, nil
}

func (b *expressionBinder) bindMultirangeUnnest(raw parser.FunctionExpr) (BoundExpr, error) {
	var small [1]functions.Argument[BoundExpr]
	var operands []functions.Argument[BoundExpr]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil { return nil, err }
		if i < len(operands) { operands[i] = arg }
	}
	if err := b.validateRangeInspectSyntax(raw); err != nil { return nil, err }
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil { return nil, err }
	var operand functions.Argument[BoundExpr]
	if len(operands) == 1 { operand = operands[0] }
	var input, result *sqltype.Descriptor
	if len(raw.Args) == 1 && operand.Usage.Type != nil {
		input = operand.Usage.Type.BaseType()
		result = sqltype.MultirangeUnnestResult(input)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if visible && name == "unnest" && len(raw.ArgNames) == 0 && len(raw.Args) == 1 && input != nil && input.IsArray() {
		return nil, expressionError("0A000", "array unnest is unsupported", raw.Pos)
	}
	if !visible || name != "unnest" || len(raw.ArgNames) != 0 || len(raw.Args) != 1 || result == nil {
		names := make([]string, len(raw.Args))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil { names[i] = b.diagnosticTypeName(arg.Usage.Type) }
			names[i] = functionNamedType(raw, i, names[i])
		}
		ambiguous := visible && name == "unnest" && len(raw.ArgNames) == 0 && len(raw.Args) == 1 && operand.Usage.Type == nil
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, ambiguous)
	}
	if err := rangeInspectDecorationError(raw); err != nil { return nil, err }
	arg, err := b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: input.Usage(), Label: "unnest"})
	if err != nil { return nil, err }
	if b.executeParameters { return nil, expressionError("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos) }
	if b.policyConsumer && b.queryFrame == 0 { return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions") }
	if err := b.stringSetAllowed(raw.Pos); err != nil { return nil, err }
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
	}
	properties := mergeExpressionProperties(ExprProperties{Volatility: Immutable, StageDependent: true}, arg.Properties())
	return multirangeUnnestExpression{expressionInfo: b.info(result.Usage(), raw.Pos, properties), args: []BoundExpr{arg}}, nil
}
