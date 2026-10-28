package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// xmlCoercionInput follows one actual selected unary coercion, not arbitrary
// expression children. Additional cast formals are checked immutable literals.
func xmlCoercionInput(node expr.Expr, position parser.Position) (expr.Expr, error) {
	invalid := func() error { return fmt.Errorf("invalid XML selected coercion spine") }
	if node == nil || node.SourcePosition() != position {
		return nil, invalid()
	}
	switch n := node.(type) {
	case CoercionExpr:
		if n.XMLSerializeResult {
			return nil, invalid()
		}
		if n.Child == nil {
			return nil, invalid()
		}
		checked, err := n.Plan.Rebind(n.Child.ResultUsage(), n.Usage, n.Mode)
		if err != nil || !checked.Equal(n.Plan) {
			return nil, invalid()
		}
		return n.Child, nil
	case DatumCastExpr:
		if err := n.ValidateStoredProvider(); err != nil {
			return nil, err
		}
		return n.Child, nil
	default:
		routine, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure)
		composite, hasChildren := node.(expr.Composite)
		if !ok || routine == nil || !hasChildren || node.SourcePosition() != position {
			return nil, invalid()
		}
		children := composite.Children()
		if len(children) == 0 {
			return nil, invalid()
		}
		for _, hidden := range children[1:] {
			if _, ok := hidden.(LiteralExpr); !ok {
				return nil, invalid()
			}
		}
		return children[0], nil
	}
}

// XMLSourceOperand removes only the source assignment coercion belonging to the
// XML opener. The written operand has its own later source position; nested XML
// operations are operands, never another conversion spine to search.
func XMLSourceOperand(node expr.Expr, opener parser.Position) (expr.Expr, error) {
	for node != nil && node.SourcePosition() == opener {
		switch converted := node.(type) {
		case CoercionExpr:
			if converted.Mode == sqltype.Explicit {
				return nil, fmt.Errorf("invalid explicit XML source coercion")
			}
		case DatumCastExpr:
			if converted.Mode == sqltype.Explicit {
				return nil, fmt.Errorf("invalid explicit XML source coercion")
			}
		}
		var err error
		node, err = xmlCoercionInput(node, opener)
		if err != nil {
			return nil, err
		}
	}
	if node == nil {
		return nil, fmt.Errorf("missing XML written operand")
	}
	return node, nil
}

// XMLSerialization follows only the ordinary selected result coercion spine.
// It stops at this occurrence's character provider, never the XML operand tree.
func XMLSerialization(result CoercionExpr) (functions.XMLSerialize, sqltype.Usage, error) {
	invalid := func() (functions.XMLSerialize, sqltype.Usage, error) {
		return functions.XMLSerialize{}, sqltype.Usage{}, fmt.Errorf("invalid XMLSERIALIZE result coercion")
	}
	if !result.XMLSerializeResult || result.Mode != sqltype.Implicit || result.Child == nil || result.Child.ResultUsage() != result.Usage {
		return invalid()
	}
	identity, err := sqltype.PlanCoercion(result.Usage, result.Usage, sqltype.Implicit)
	if err != nil || !identity.Equal(result.Plan) {
		return invalid()
	}
	for node := result.Child; node != nil; {
		if source, ok := node.(functions.XMLSerialize); ok {
			if source.SourcePosition() != result.SourcePosition() || source.Syntax.Pos != result.SourcePosition() || source.Syntax.Child == nil || source.Syntax.Document != source.Document || source.Syntax.Indent != source.Indent {
				return invalid()
			}
			written, err := XMLSourceOperand(source.Child, source.Pos)
			if err != nil || written.SourcePosition() != source.Syntax.Child.SourcePosition() {
				return invalid()
			}
			if err := source.ValidateStoredProvider(); err != nil {
				return functions.XMLSerialize{}, sqltype.Usage{}, err
			}
			return source, result.Usage, nil
		}
		switch converted := node.(type) {
		case CoercionExpr:
			if converted.Mode != sqltype.Implicit {
				return invalid()
			}
		case DatumCastExpr:
			if converted.Mode != sqltype.Implicit {
				return invalid()
			}
		}
		next, err := xmlCoercionInput(node, result.Pos)
		if err != nil {
			return invalid()
		}
		node = next
	}
	return invalid()
}

func XMLSerializationRendering(result CoercionExpr) (expr.Rendering, error) {
	source, target, err := XMLSerialization(result)
	if err != nil {
		return expr.Rendering{}, err
	}
	rendering, err := source.Render()
	if err != nil {
		return expr.Rendering{}, err
	}
	rendering.OwnedReturning = target
	return rendering, nil
}
