package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindNumericFormatting binds to_number. It runs after the engine's shared
// call gates; builtin visibility follows them, as in the final name switch.
func bindNumericFormatting(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	var small [4]Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringTextArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if ResolveNumericFormatting(visible, raw.Variadic, operands) != Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	return numericFormattingOperands(b, raw, operands, b.Type(sqltype.Text))
}

// numericFormattingOperands finishes numeric to_char/to_number over operands
// already transformed by the caller's resolver, retaining argument error
// precedence and parameter occurrences.
func numericFormattingOperands(b Binder, raw parser.FunctionExpr, operands []Argument, source *sqltype.Descriptor) (expr.Expr, error) {
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
