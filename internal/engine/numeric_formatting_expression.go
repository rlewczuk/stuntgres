package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindNumericFormatting(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [4]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringTextArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if functions.ResolveNumericFormatting(visible, raw.Variadic, operands) != functions.Resolved {
		return nil, b.numericFormattingSignature(raw, operands)
	}
	return b.numericFormattingOperands(raw, operands, b.typ(sqltype.Text))
}
func (b *expressionBinder) numericFormattingSignature(raw parser.FunctionExpr, operands []functions.Argument) error {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = datetimeFormattingTypeName(o.Usage.Type)
	}
	return b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}

// Numeric to_char arrives with already-transformed operands from the temporal
// resolver, retaining argument error precedence and parameter occurrences.
func (b *expressionBinder) numericFormattingOperands(raw parser.FunctionExpr, operands []functions.Argument, source *sqltype.Descriptor) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	text := source.Usage()
	if source.ID() != sqltype.Text {
		text = b.typ(sqltype.Text).Usage()
	}
	args, err := functions.CoerceNumericFormattingArguments(b, operands, source.Usage(), text)
	if err != nil {
		return nil, err
	}
	properties := expr.IntrinsicProperties(functions.NumericFormattingIntrinsicRule())
	for _, argument := range args {
		p := argument.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	result := text
	if id := functions.NumericFormattingResult(name); id != sqltype.Text {
		result = b.typ(id).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	collated, err := b.collatedInfo(b.info(text, raw.Pos, properties), args[:]...)
	if err != nil {
		return nil, err
	}
	if result.Type.ID() == sqltype.Text {
		info = collated
	}
	var constant *string
	if literal, ok := args[1].(literalExpression); ok && !literal.value.IsNull() {
		text, err := literal.value.Text()
		if err != nil {
			return nil, err
		}
		constant = &text
	}
	node, err := functions.NewNumericFormatting(b.ctx, info, name, args, constant)
	if err != nil {
		return nil, err
	}
	info, err = b.builtinRoutineInfo(info, datetimeRoutineProvider, name, source.ID(), sqltype.Text)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
