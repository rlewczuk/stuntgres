package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayMembershipName(name string) bool { return name == "@>" || name == "<@" || name == "&&" }

func (b *expressionBinder) bindArrayMembership(raw parser.OperatorExpr) (expr.Expr, error) {
	if len(raw.Args) != 2 {
		return nil, fmt.Errorf("stuntgres: invalid membership arity")
	}
	var operands [2]comparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	return b.finishArrayMembership(raw, operands)
}

func (b *expressionBinder) finishArrayMembership(raw parser.OperatorExpr, operands [2]comparisonOperand) (expr.Expr, error) {
	left, right := operands[0].descriptor(), operands[1].descriptor()
	visible, err := b.builtinVisible(raw.Name, analyze.OperatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functions.ArrayOperatorError(b, left, right, raw)
	}
	if left == nil && right == nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		d := expr.ErrorAt("42725", "operator is not unique: unknown "+name+" unknown", raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
		return nil, d
	}
	if functions.JSONBContainmentName(raw.Name.Parts[len(raw.Name.Parts)-1].Text) &&
		(left != nil && left.BaseType().ID() == sqltype.JSONB || right != nil && right.BaseType().ID() == sqltype.JSONB) {
		return functions.BindJSONBContainment(b, raw, [2]functions.Argument{operandArgument(operands[0]), operandArgument(operands[1])})
	}
	// ACL arrays select the fixed array/item signature for an unknown RHS;
	// a known array RHS still belongs to the generic array/array operator.
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "@>" && (right != nil && right.BaseType().ID() == sqltype.ACLItem && (left == nil || left.BaseType().ID() == sqltype.ACLItemArray) || right == nil && left != nil && left.BaseType().ID() == sqltype.ACLItemArray) {
		namespace := ""
		if len(raw.Name.Parts) == 2 {
			namespace = raw.Name.Parts[0].Text
		}
		return functions.BindACLContains(b, [2]functions.Argument{operandArgument(operands[0]), operandArgument(operands[1])}, namespace, raw.Pos)
	}
	var selected *sqltype.Descriptor
	for _, d := range []*sqltype.Descriptor{left, right} {
		if d == nil {
			continue
		}
		base := d.BaseType()
		if !base.IsArray() || selected != nil && selected != base {
			return nil, functions.ArrayOperatorError(b, left, right, raw)
		}
		selected = base
	}
	args := make([]expr.Expr, 2)
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, selected.Usage(), raw.Name.Parts[len(raw.Name.Parts)-1].Text)
		if err != nil {
			return nil, err
		}
	}
	return functions.ArraySearchNode(b, raw.Name.Parts[len(raw.Name.Parts)-1].Text, raw.Name, raw.Pos, b.typ(sqltype.Bool).Usage(), args)
}

// This is the array branch of the existing || selector. Its children have
// already been analyzed by bindStringConcat; no child is rebound here.
func (b *expressionBinder) selectArrayConcat(raw parser.OperatorExpr, operands [2]functions.Argument, visible bool) (expr.Expr, bool, error) {
	left, right := operands[0].Usage.Type, operands[1].Usage.Type
	array := func(d *sqltype.Descriptor) bool { return d != nil && d.BaseType().IsArray() }
	if !array(left) && !array(right) {
		return nil, false, nil
	}
	signature := func() (expr.Expr, error) { return nil, functions.ArrayOperatorError(b, left, right, raw) }
	if !visible {
		e, err := signature()
		return e, true, err
	}
	kind := functions.ArrayConcatenate
	if array(left) && right != nil && !array(right) {
		kind = functions.ArrayAppend
	}
	if array(right) && left != nil && !array(left) {
		kind = functions.ArrayPrepend
	}
	if array(left) && array(right) {
		if right.BaseType().Element() == left {
			kind = functions.ArrayPrepend
		} else if left.BaseType().Element() == right {
			kind = functions.ArrayAppend
		}
	}
	call := parser.FunctionExpr{Name: raw.Name, Pos: raw.Pos}
	e, err := functions.FinishArrayTransform(b, call, kind, operands[:], [3]uint8{0, 1, 2}, [3]string{}, true, signature)
	return e, true, err
}
