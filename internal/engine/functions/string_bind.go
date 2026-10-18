package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// stringNames are the string functions dispatched by exact spelling after the
// shared call gates. PostgreSQL has no trim function: trim's arguments are
// analyzed before its missing-signature error.
var stringNames = [...]string{
	"left", "right", "to_bin", "to_oct", "to_hex", "md5", "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "trim",
	"casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "position", "strpos",
	"overlay", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii",
	"unistr", "unicode_assigned", "split_part", "string_to_array",
}

// bindStringName runs after the shared call gates; builtin visibility follows
// them, as it did in the engine's final name switch.
func bindStringName(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return BindString(b, raw, visible && raw.Name.Parts[len(raw.Name.Parts)-1].Text != "trim")
}

// bindStringToTable binds the string_to_table set-returning function, which
// the analyzer admits only where its clause and consumer allow sets.
func bindStringToTable(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	node, err := BindString(b, raw, visible)
	if err != nil {
		return nil, err
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	return node, nil
}

// bindParseIdent claims parse_ident in any spelling before the shared gates;
// it accepts its str/strict labels, and only the exact spelling is visible.
func bindParseIdent(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return BindString(b, raw, visible && raw.Name.Parts[len(raw.Name.Parts)-1].Text == "parse_ident")
}

// bindLowerUpper binds lower and upper, which share text and range overloads.
// Operands are analyzed with string intents; visibility follows the shared
// call envelope. Range operands are finished by the range family.
func bindLowerUpper(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]Argument
	var operands []Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, StringArgumentAnalysis(name, i))
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = operand
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return FinishString(b, raw, visible, operands)
}

// BindString analyzes and binds a string function call.
func BindString(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]Argument
	var operands []Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, r := range raw.Args {
		request := StringArgumentAnalysis(name, i)
		if name == "parse_ident" && stringIdentStrictArgument(raw, i) {
			request = AnalysisRequest{Intent: StringOtherArgument}
		}
		o, err := b.AnalyzeArgument(r, request)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if name == "parse_ident" || strings.EqualFold(name, "lower") || strings.EqualFold(name, "upper") {
		if err := ValidateCallSyntax(b, raw); err != nil {
			return nil, err
		}
		return FinishString(b, raw, visible, operands)
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	return FinishString(b, raw, visible, operands)
}

// Reuse already transformed operands when selected lower/upper routing shares
// the text provider. Other string providers retain their existing analysis.
// FinishString selects and builds a string call over analyzed operands; a
// single range operand of lower/upper goes to the range family. The binary
// substring route reuses its transformed operands.
func FinishString(b Binder, raw parser.FunctionExpr, visible bool, operands []Argument) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if (name == "lower" || name == "upper") && len(operands) == 1 && sqltype.IsRangeInspectType(operands[0].Usage.Type) {
		return FinishRangeInspect(b, raw, visible, operands)
	}
	if sqltype.StringBaseConversion(name) {
		return finishStringNumericBase(b, raw, visible, operands)
	}
	var encodingName sqltype.Usage
	if visible && name == "length" && len(operands) == 2 {
		source, encoding := operands[0].Usage.Type, operands[1].Usage.Type
		if (source == nil || source.BaseType().ID() == sqltype.Bytea) && encoding != nil {
			encodingName = b.Type(sqltype.Name).Usage()
		}
	}
	if StringEncodingLength(name, visible, operands, encodingName) {
		if b.DefaultConsumer() {
			return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
		}
		return nil, expr.ErrorAt("0A000", "encoding-aware length is unsupported", raw.Pos)
	}
	bitCandidate := BinaryBitCandidate(name, raw.Variadic, operands)
	if visible && StringBinaryCandidate(name, raw.Variadic, operands) {
		var binaryTypes BinaryTypes
		family := sqltype.Bytea
		if bitCandidate {
			family = sqltype.Bit
		}
		SupplyBinaryTypes(b, BinaryArgumentDemand(name, family, operands, true), &binaryTypes)
		if BinaryArgumentsViable(name, family, operands, &binaryTypes) {
			if b.DefaultConsumer() && !bitCandidate {
				return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
			}
			syntax := raw.Syntax
			if raw.StringSyntax == parser.TrimStringSyntax {
				syntax = parser.SQLFunctionSyntax
			}
			return FinishBinary(b, name, family, raw.Pos, operands, syntax, &binaryTypes)
		}
	}
	targets := StringTargets{Text: b.Type(sqltype.Text).Usage()}
	integer, nameTarget := StringTargetDemand(name, len(operands))
	if integer {
		targets.Integer = b.Type(sqltype.Int4).Usage()
	}
	if nameTarget && name == "to_ascii" {
		targets.Name = b.Type(sqltype.Name).Usage()
	}
	if name == "parse_ident" {
		targets.Boolean = b.Type(sqltype.Bool).Usage()
	}
	textInspection := strings.EqualFold(name, "lower") || strings.EqualFold(name, "upper")
	selectionVisible := visible && (!textInspection || len(raw.ArgNames) == 0)
	var selection StringSelection
	var resolution Resolution
	if name == "parse_ident" {
		selection, resolution = selectStringIdent(raw, visible, operands, targets)
	} else {
		selection, resolution = SelectStringFunction(name, selectionVisible, raw.Variadic, operands, targets)
	}
	if resolution != Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		for i := range names {
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if name == "parse_ident" || textInspection {
		if err := CallDecorationError(raw); err != nil {
			return nil, err
		}
	}
	args, err := CoerceStringArguments(b, operands, selection, targets)
	if err != nil {
		return nil, err
	}
	result := b.Type(selection.Result).Usage()
	if selection.Array {
		result = b.Type(result.Type.AssociatedArray()).Usage()
	}
	if name == "position" && raw.Syntax == parser.SQLFunctionSyntax {
		raw.StringSyntax = parser.PositionStringSyntax
	}
	if name == "overlay" && raw.Syntax == parser.SQLFunctionSyntax {
		raw.StringSyntax = parser.OverlayStringSyntax
	}
	if name == "substring" && raw.Syntax == parser.SQLFunctionSyntax {
		raw.StringSyntax = parser.SubstringStringSyntax
	}
	return StringNode(b, name, result, raw.Pos, args, selection.Textual, raw.StringSyntax)
}
