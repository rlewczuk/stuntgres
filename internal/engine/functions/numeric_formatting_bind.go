package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// NumericFormattingOperands finishes numeric to_char/to_number over operands
// already transformed by the caller's resolver, retaining argument error
// precedence and parameter occurrences.
func NumericFormattingOperands(b Binder, raw parser.FunctionExpr, operands []Argument, source *sqltype.Descriptor) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	text := source.Usage()
	if source.ID() != sqltype.Text {
		text = b.Type(sqltype.Text).Usage()
	}
	args, err := CoerceNumericFormattingArguments(b, operands, source.Usage(), text)
	if err != nil {
		return nil, err
	}
	properties := expr.IntrinsicProperties(NumericFormattingIntrinsicRule())
	for _, argument := range args {
		properties = expr.MergeProperties(properties, argument.Properties())
	}
	result := text
	if id := NumericFormattingResult(name); id != sqltype.Text {
		result = b.Type(id).Usage()
	}
	info := b.Info(result, raw.Pos, properties)
	collated, err := b.CollatedInfo(b.Info(text, raw.Pos, properties), args[:]...)
	if err != nil {
		return nil, err
	}
	if result.Type.ID() == sqltype.Text {
		info = collated
	}
	constant, err := constantTemplate(b, args[1])
	if err != nil {
		return nil, err
	}
	node, err := NewNumericFormatting(b.Context(), info, name, args, constant)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, source.ID(), sqltype.Text)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}

// constantTemplate returns a non-NULL literal formatting template, which
// bind time compiles once.
func constantTemplate(b Binder, arg expr.Expr) (*string, error) {
	value, ok := b.ConstantValue(arg)
	if !ok || value.IsNull() {
		return nil, nil
	}
	text, err := value.Text()
	if err != nil {
		return nil, err
	}
	return &text, nil
}
