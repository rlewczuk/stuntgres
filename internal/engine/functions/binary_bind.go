package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var binaryNames = [...]string{
	"convert", "convert_from", "convert_to", "encode", "decode", "substring", "substr",
	"crc32", "crc32c", "sha224", "sha256", "sha384", "sha512",
	"bit_count", "get_bit", "get_byte", "set_bit", "set_byte",
}

// bindBinary runs after the engine's shared call gates (SharedGates).
// Builtin visibility follows the gates, as it did in the engine's final name
// switch.
func bindBinary(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return BindBinary(b, raw, visible)
}

// BindBinary binds a binary data call once visibility is known. Text
// overloads of substring and substr route to the POSIX and SIMILAR substrings
// or to the string family.
func BindBinary(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operands []Argument
	if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, BinaryArgumentAnalysis(name, i))
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	access := name == "get_bit" || name == "set_bit"
	if b.DefaultConsumer() && !access && !BinaryDefaultAllowed(name, len(raw.Args)) && !BinaryBitCandidate(name, raw.Variadic, operands) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	prepared := PrepareBinary(name, visible, raw.Variadic, b.DefaultConsumer(), operands)
	var types BinaryTypes
	SupplyBinaryTypes(b, prepared.Demand, &types)
	selection := ResolveBinary(prepared, name, operands, &types)
	if selection.Resolution != Resolved {
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = "unknown"
			if operand.Usage.Type != nil {
				names[i] = operand.Usage.Type.DiagnosticName()
			}
		}
		if selection.Resolution == Ambiguous {
			return nil, ResolutionError(raw.Name, raw.Pos, names, true)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if b.DefaultConsumer() && access && selection.Family != sqltype.Bit {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	switch selection.Route {
	case BinaryFunctionsUnsupported:
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	case BinaryPosixSubstring:
		return BindPosixSubstring(b, raw, operands)
	case BinarySimilarSubstring:
		return BindSimilarSubstring(b, raw, operands)
	case BinaryTextSubstring:
		return FinishString(b, raw, visible, operands)
	case BinaryTextUnsupported:
		return nil, expr.ErrorAt("0A000", "text overloads of "+name+" are unsupported", raw.Pos)
	}
	return FinishBinary(b, name, selection.Family, raw.Pos, operands, raw.Syntax, &types)
}

// FinishBinary coerces the operands of a selected bytea, bit-string or ||
// overload and builds its node. The string family finishes its binary
// candidates here too.
func FinishBinary(b Binder, name string, family sqltype.ID, pos parser.Position, operands []Argument, syntax parser.FunctionSyntax, types *BinaryTypes) (expr.Expr, error) {
	SupplyBinaryTypes(b, BinaryArgumentDemand(name, family, operands, false), types)
	args, err := CoerceBinaryArguments(b, name, family, operands, types)
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	if BinaryIntrinsicStable(name) {
		properties.Volatility = expr.Stable
	}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	SupplyBinaryTypes(b, BinaryResultDemand(name, family), types)
	id := BinaryResultID(name, family)
	usage := types.Usage(id)
	info := b.Info(usage, pos, properties)
	if BinaryIntrinsicStable(name) {
		demand := true
		for _, arg := range args {
			if b.LiteralNull(arg) {
				demand = false
				break
			}
		}
		if demand {
			b.NoteVisibilitySnapshot()
		}
	}
	inputInfo, err := b.CollatedInfo(b.Info(types.Text, pos, properties), args...)
	if err != nil {
		return nil, err
	}
	if id == sqltype.Text {
		info = inputInfo
	}
	node, err := NewBinary(info, name, args, syntax)
	if err != nil {
		return nil, err
	}
	var formal [4]sqltype.ID
	for i := range args {
		formal[i] = BinaryArgumentID(name, i, family)
	}
	if name == "||" {
		info, err = b.BuiltinOperatorRoutineInfo(info, name, formal[0], formal[1])
	} else {
		info, err = b.BuiltinRoutineInfo(info, BinaryRoutineProvider, name, formal[:len(args)]...)
	}
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
