package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonExtractionName(name string) bool {
	return name == "->" || name == "->>" || name == "#>" || name == "#>>"
}
func (b *expressionBinder) bindJSONExtraction(raw parser.OperatorExpr) (BoundExpr, error) {
	var operands [2]comparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < 2 {
			operands[i] = o
		}
	}
	left, right := operands[0].descriptor(), operands[1].descriptor()
	signature := func() error { return b.arrayMembershipError(left, right, raw) }
	if len(raw.Args) != 2 {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, signature()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	rhs := b.typ(sqltype.Text).Usage()
	if name == "#>" || name == "#>>" {
		a, ok := b.bindingCatalog().LookupArray(b.typ(sqltype.Text))
		if !ok {
			return nil, fmt.Errorf("missing text array")
		}
		rhs = a.Usage()
	} else if right != nil && (right.BaseType().ID() == sqltype.Int2 || right.BaseType().ID() == sqltype.Int4) {
		rhs = b.typ(sqltype.Int4).Usage()
	}
	document := b.typ(sqltype.JSON).Usage()
	if left != nil && left.BaseType().ID() == sqltype.JSONB {
		document = b.typ(sqltype.JSONB).Usage()
	}
	targets := [2]sqltype.Usage{document, rhs}
	for i, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return nil, signature()
			}
		}
	}
	if left == nil {
		return nil, b.comparisonError(sqlError("42725", "operator is not unique"), left, right, raw)
	}
	var args [2]BoundExpr
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, targets[i], name)
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[i].Properties())
	}
	result := document
	if name == "->>" || name == "#>>" {
		result = b.typ(sqltype.Text).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	node, err := functions.NewBinary(info, name, args[:], parser.OrdinaryFunctionSyntax)
	if err != nil {
		return nil, err
	}
	info, err = b.builtinOperatorRoutineInfo(info, name, document.Type.ID(), rhs.Type.ID())
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
