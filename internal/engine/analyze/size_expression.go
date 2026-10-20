package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Size overloads have no preferred numeric candidate. Analyze children before
// resolving signatures, while retaining unknowns for occurrence-owned inference.
func (b *Binder) bindSize(raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operand functions.Argument
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 1 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := functions.AnalyzeSizeArgument(b, name, child)
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = o
		} else if names != nil {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	var types functions.SizeTypes
	demand := functions.NeededSizeTypes(name, visible, len(raw.Args), operand.Usage)
	if demand&functions.SizeText != 0 {
		types.Text = b.Type(sqltype.Text)
	}
	if demand&functions.SizeInt8 != 0 {
		types.Int8 = b.Type(sqltype.Int8)
	}
	if demand&functions.SizeNumeric != 0 {
		types.Numeric = b.Type(sqltype.Numeric)
	}
	target, resolution := functions.SelectSize(name, visible, len(raw.Args), operand.Usage, types)
	if resolution != functions.Resolved {
		if len(raw.Args) == 1 {
			typ := "unknown"
			if d := operand.Usage.Type; d != nil {
				typ = d.DiagnosticName()
			}
			names = []string{typ}
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, resolution == functions.Ambiguous)
	}
	arg, err := functions.CoerceSizeArgument(b, operand, target.Usage())
	if err != nil {
		return nil, err
	}
	id := functions.SizeResult(name)
	var resultType *sqltype.Descriptor
	switch id {
	case sqltype.Text:
		resultType = types.Text
	case sqltype.Int8:
		resultType = types.Int8
	}
	if resultType == nil {
		resultType = b.Type(id)
	}
	info := b.Info(resultType.Usage(), raw.Pos, arg.Properties())
	node, err := functions.NewSize(info, name, arg, target.Usage())
	if err != nil {
		return nil, err
	}
	info, err = b.builtinRoutineInfo(info, functions.BinaryRoutineProvider, name, target.ID())
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
