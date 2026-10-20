package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// stringNode builds a string node and records what only the analyzer owns:
// catalog-identifier output of ||, and polymorphic body casts.
func (b *expressionBinder) stringNode(name string, result sqltype.Usage, pos parser.Position, args []expr.Expr, textual [4]bool, syntax parser.StringSyntax) (expr.Expr, error) {
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
			if established := b.params.parameters[parameter.Number]; established.Type != nil {
				policy.expected = established
			}
		}
	}
	return b.comparisonOperandPolicy(raw, policy)
}

func (b *expressionBinder) bindStringPrefix(raw parser.OperatorExpr) (expr.Expr, error) {
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
	args := make([]expr.Expr, 2)
	for i, o := range operands {
		args[i], err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.typ(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
	}
	return b.stringNode("starts_with", b.typ(sqltype.Bool).Usage(), raw.Pos, args, [4]bool{true, true}, parser.PrefixStringSyntax)
}
func (b *expressionBinder) bindStringConcat(raw parser.OperatorExpr) (expr.Expr, error) {
	if len(raw.Args) != 2 {
		return nil, expr.ErrorAt("42883", "operator does not exist: ||", raw.Pos)
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
	if node, selected, err := functions.SelectJSONBConcat(b, raw, operands, visible); selected {
		return node, err
	}
	textual, binary, resolution := functions.SelectStringConcat(visible, types)
	if binary != 0 {
		var binaryTypes functions.BinaryTypes
		return functions.FinishBinary(b, "||", binary, raw.Pos, operands[:], parser.OrdinaryFunctionSyntax, &binaryTypes)
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
		d := expr.ErrorAt("42725", fmt.Sprintf("operator is not unique: %s %s %s", typeName(types[0]), objectNameText(raw.Name), typeName(types[1])), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
		return nil, d
	}
	args := make([]expr.Expr, 2)
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
