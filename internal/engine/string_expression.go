package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// stringNode builds a string node and records what only the analyzer owns:
// catalog-identifier output of ||, and polymorphic body casts.
func (b *expressionBinder) stringNode(name string, result sqltype.Usage, pos parser.Position, args []BoundExpr, textual [4]bool, syntax parser.StringSyntax) (BoundExpr, error) {
	node, err := functions.StringNode(b, name, result, pos, args, textual, syntax)
	if err != nil {
		return nil, err
	}
	for i, arg := range args {
		if functions.StringCatalogOutput(name, textual[i], arg.ResultUsage()) {
			b.regclassConcat = true
		}
	}
	if err := b.capturePolymorphicBody(node); err != nil {
		return nil, err
	}
	return node, nil
}

// COLLATE resolves before overload finalization. Contextualize only textual
// roles, preserving each established parameter's input descriptor.
func (b *expressionBinder) stringOperand(raw parser.Expr, textual bool) (comparisonOperand, error) {
	policy := expressionPolicy{context: projectionContext, stringInput: textual}
	if _, collated := raw.(parser.CollateExpr); collated && textual {
		policy.expected = b.typ(sqltype.Text).Usage()
		child := raw
		for {
			collation, ok := child.(parser.CollateExpr)
			if !ok {
				break
			}
			child = collation.Child
		}
		if parameter, ok := child.(parser.ParameterExpr); ok {
			if established := b.parameters[parameter.Number]; established.Type != nil {
				policy.expected = established
			}
		}
	}
	return b.comparisonOperandPolicy(raw, policy)
}

func (b *expressionBinder) bindStringFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, r := range raw.Args {
		request := functions.StringArgumentAnalysis(name, i)
		if name == "parse_ident" && stringIdentStrictArgument(raw, i) {
			request = functions.AnalysisRequest{Intent: functions.StringOtherArgument}
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
		if err := functions.ValidateCallSyntax(b, raw); err != nil {
			return nil, err
		}
		return b.finishStringFunction(raw, visible, operands)
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	return b.finishStringFunction(raw, visible, operands)
}

// bindLowerUpper binds lower and upper, which share text and range overloads.
// Operands are analyzed with string intents; visibility follows the shared
// call envelope. Range operands are finished by the range family.
func (b *expressionBinder) bindLowerUpper(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, functions.StringArgumentAnalysis(name, i))
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = operand
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	return b.finishStringFunction(raw, visible, operands)
}

// Reuse already transformed operands when selected lower/upper routing shares
// the text provider. Other string providers retain their existing analysis.
func (b *expressionBinder) finishStringFunction(raw parser.FunctionExpr, visible bool, operands []functions.Argument) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if (name == "lower" || name == "upper") && len(operands) == 1 && sqltype.IsRangeInspectType(operands[0].Usage.Type) {
		return functions.FinishRangeInspect(b, raw, visible, operands)
	}
	if sqltype.StringBaseConversion(name) {
		return b.finishStringNumericBase(raw, visible, operands)
	}
	var encodingName sqltype.Usage
	if visible && name == "length" && len(operands) == 2 {
		source, encoding := operands[0].Usage.Type, operands[1].Usage.Type
		if (source == nil || source.BaseType().ID() == sqltype.Bytea) && encoding != nil {
			encodingName = b.typ(sqltype.Name).Usage()
		}
	}
	if functions.StringEncodingLength(name, visible, operands, encodingName) {
		if b.defaultConsumer {
			return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
		}
		return nil, expressionError("0A000", "encoding-aware length is unsupported", raw.Pos)
	}
	bitCandidate := functions.BinaryBitCandidate(name, raw.Variadic, operands)
	if visible && functions.StringBinaryCandidate(name, raw.Variadic, operands) {
		var binaryTypes functions.BinaryTypes
		family := sqltype.Bytea
		if bitCandidate {
			family = sqltype.Bit
		}
		functions.SupplyBinaryTypes(b, functions.BinaryArgumentDemand(name, family, operands, true), &binaryTypes)
		if functions.BinaryArgumentsViable(name, family, operands, &binaryTypes) {
			if b.defaultConsumer && !bitCandidate {
				return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
			}
			syntax := raw.Syntax
			if raw.StringSyntax == parser.TrimStringSyntax {
				syntax = parser.SQLFunctionSyntax
			}
			return b.finishBinary(name, family, raw.Pos, operands, syntax, &binaryTypes)
		}
	}
	targets := functions.StringTargets{Text: b.typ(sqltype.Text).Usage()}
	integer, nameTarget := functions.StringTargetDemand(name, len(operands))
	if integer {
		targets.Integer = b.typ(sqltype.Int4).Usage()
	}
	if nameTarget && name == "to_ascii" {
		targets.Name = b.typ(sqltype.Name).Usage()
	}
	if name == "parse_ident" {
		targets.Boolean = b.typ(sqltype.Bool).Usage()
	}
	textInspection := strings.EqualFold(name, "lower") || strings.EqualFold(name, "upper")
	selectionVisible := visible && (!textInspection || len(raw.ArgNames) == 0)
	var selection functions.StringSelection
	var resolution functions.Resolution
	if name == "parse_ident" {
		selection, resolution = selectStringIdent(raw, visible, operands, targets)
	} else {
		selection, resolution = functions.SelectStringFunction(name, selectionVisible, raw.Variadic, operands, targets)
	}
	if resolution != functions.Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		for i := range names {
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if name == "parse_ident" || textInspection {
		if err := functions.CallDecorationError(raw); err != nil {
			return nil, err
		}
	}
	args, err := functions.CoerceStringArguments(b, operands, selection, targets)
	if err != nil {
		return nil, err
	}
	result := b.typ(selection.Result).Usage()
	if selection.Array {
		result = b.typ(result.Type.AssociatedArray()).Usage()
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
	return b.stringNode(name, result, raw.Pos, args, selection.Textual, raw.StringSyntax)
}

func (b *expressionBinder) bindStringPrefix(raw parser.OperatorExpr) (BoundExpr, error) {
	var operands [2]functions.Argument
	var types [2]*sqltype.Descriptor
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.StringTextArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i], types[i] = o, o.Usage.Type
		}
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !functions.StringPrefixViable(visible, len(raw.Args), types) {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), types[0], types[1], raw)
	}
	args := make([]BoundExpr, 2)
	for i, o := range operands {
		args[i], err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.typ(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
	}
	return b.stringNode("starts_with", b.typ(sqltype.Bool).Usage(), raw.Pos, args, [4]bool{true, true}, parser.PrefixStringSyntax)
}
func (b *expressionBinder) bindStringConcat(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 {
		return nil, expressionError("42883", "operator does not exist: ||", raw.Pos)
	}
	var operands [2]functions.Argument
	var types [2]*sqltype.Descriptor
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
		types[i] = o.Usage.Type
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if node, selected, err := b.selectArrayConcat(raw, operands, visible); selected {
		return node, err
	}
	if node, selected, err := b.selectJSONBConcat(raw, operands, visible); selected {
		return node, err
	}
	textual, binary, resolution := functions.SelectStringConcat(visible, types)
	if binary != 0 {
		var binaryTypes functions.BinaryTypes
		return b.finishBinary("||", binary, raw.Pos, operands[:], parser.OrdinaryFunctionSyntax, &binaryTypes)
	}
	if resolution != functions.Resolved {
		if resolution == functions.Missing {
			return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), types[0], types[1], raw)
		}
		typeName := func(t *sqltype.Descriptor) string {
			if t == nil {
				return "unknown"
			}
			return t.DiagnosticName()
		}
		d := expressionError("42725", fmt.Sprintf("operator is not unique: %s %s %s", typeName(types[0]), objectNameText(raw.Name), typeName(types[1])), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
		return nil, d
	}
	args := make([]BoundExpr, 2)
	for i, o := range operands {
		if o.Usage.Type == nil {
			args[i], err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.typ(sqltype.Text).Usage()})
			if err != nil {
				return nil, err
			}
		} else {
			args[i] = o.Expression
		}
	}
	return b.stringNode("||", b.typ(sqltype.Text).Usage(), raw.Pos, args, [4]bool{textual[0], textual[1]}, parser.OrdinaryStringSyntax)
}

// Both PostgreSQL presentation and native replay preserve special syntax, but
// each renderer owns its child formatting and literal transport.
func stringSpecialSQL(n functions.String, parts []string) (string, error) {
	source, set := parts[0], ""
	if len(parts) > 1 {
		set = parts[1]
	}
	form := ""
	if n.Syntax == parser.NormalizeStringSyntax || n.Syntax == parser.NormalizedStringSyntax {
		if len(n.Args) == 2 {
			literal, ok := n.Args[1].(literalExpression)
			if !ok {
				return "", fmt.Errorf("stuntgres: invalid normalization syntax form")
			}
			var err error
			form, err = literal.value.Text()
			if err != nil {
				return "", err
			}
		}
	}
	switch n.Syntax {
	case parser.TrimStringSyntax:
		return trimSyntaxSQL(n.Plan.Name(), source, set, len(n.Args))
	case parser.PositionStringSyntax:
		if n.Plan.Name() != "position" || len(n.Args) != 2 {
			return "", fmt.Errorf("stuntgres: invalid POSITION syntax provider")
		}
		return renderBinaryCall(n.Plan.Name(), parser.SQLFunctionSyntax, parts, n.Plan.Name(), func(s string) (string, error) { return s, nil })
	case parser.OverlayStringSyntax:
		return renderBinaryCall(n.Plan.Name(), parser.SQLFunctionSyntax, parts, n.Plan.Name(), func(s string) (string, error) { return s, nil })
	case parser.SubstringStringSyntax:
		return renderBinaryCall(n.Plan.Name(), parser.SQLFunctionSyntax, parts, n.Plan.Name(), func(s string) (string, error) { return s, nil })
	case parser.PrefixStringSyntax:
		if n.Plan.Name() != "starts_with" || len(parts) != 2 {
			return "", fmt.Errorf("stuntgres: invalid prefix syntax provider")
		}
		return "(" + source + " OPERATOR(pg_catalog.^@) " + set + ")", nil
	case parser.NormalizeStringSyntax:
		if form != "" {
			source += ", " + form
		}
		return "NORMALIZE(" + source + ")", nil
	case parser.NormalizedStringSyntax:
		if form != "" {
			form += " "
		}
		return "(" + source + " IS " + form + "NORMALIZED)", nil
	default:
		return "", fmt.Errorf("stuntgres: invalid special string syntax")
	}
}

func trimSyntaxSQL(name, source, set string, count int) (string, error) {
	direction := ""
	switch name {
	case "btrim":
		direction = "BOTH"
	case "ltrim":
		direction = "LEADING"
	case "rtrim":
		direction = "TRAILING"
	default:
		return "", fmt.Errorf("stuntgres: invalid trim syntax provider")
	}
	if count == 2 {
		direction += " " + set
	} else if count != 1 {
		return "", fmt.Errorf("stuntgres: invalid trim syntax arity")
	}
	return "TRIM(" + direction + " FROM " + source + ")", nil
}
