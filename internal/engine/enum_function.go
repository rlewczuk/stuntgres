package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type enumFunctionExpression struct {
	expressionInfo
	plan     sqltype.EnumSupportPlan
	args     []BoundExpr
	enumName string
}

func (e enumFunctionExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	value, err := e.plan.Evaluate(c.Context, values[0], values[1])
	if d, ok := err.(*sqlstate.Error); ok && d.Code == "55000" {
		name := e.enumName
		if c.Input.TypeDiagnosticName != nil {
			if current := c.Input.TypeDiagnosticName(e.plan.Enum().ID()); current != "" {
				name = current
			}
		}
		copy := *d
		copy.Message = fmt.Sprintf("enum %s contains no values", name)
		return sqltype.Value{}, &copy
	}
	return value, err
}
func (b *expressionBinder) bindEnumFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands := make([]comparisonOperand, len(raw.Args))
	names := make([]string, len(raw.Args))
	var selected *sqltype.Descriptor
	valid := visible && (len(raw.Args) == 1 || name == "enum_range" && len(raw.Args) == 2)
	for i, r := range raw.Args {
		operand, err := b.comparisonOperandPolicy(r, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
		names[i] = "unknown"
		if typ := operand.descriptor(); typ != nil {
			names[i] = b.diagnosticTypeName(typ)
			if !typ.IsEnum() || selected != nil && selected != typ {
				valid = false
			} else {
				selected = typ
			}
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	if !valid || selected == nil {
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	args := make([]BoundExpr, len(operands))
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	usages := make([]sqltype.Usage, len(operands))
	properties := ExprProperties{Volatility: Stable}
	for i, operand := range operands {
		arg, err := b.coerceConditional(operand, selected.Usage(), name)
		if err != nil {
			return nil, err
		}
		args[i] = arg
		usages[i] = arg.ResultUsage()
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	result := selected.Usage()
	if name == "enum_range" {
		array, ok := b.bindingCatalog().LookupArray(selected)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing enum array association")
		}
		result = array.Usage()
	}
	plan, err := sqltype.BindEnumSupport(name, result, usages...)
	if err != nil {
		return nil, err
	}
	return enumFunctionExpression{expressionInfo: b.info(result, raw.Pos, properties), plan: plan, args: args, enumName: b.diagnosticTypeName(selected)}, nil
}
