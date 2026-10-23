package sqltype

import "stuntgres/internal/sqlstate"

// Numeric kernels require typed headers (modulo also uses typed comparison).
// This immutable private provider never supplies SQL/catalog identity: results
// retain only their exact JSON decimal fields in jsonpathNumericResult.
var jsonpathNumericDescriptor = Descriptor{id: Numeric}

func jsonpathNumericOperand(n jsonbDecimal) Value {
	v := n.value()
	v.typ = &jsonpathNumericDescriptor
	return v
}

// Binary arithmetic needs only a singleton candidate and a saturated count, but
// must finish both operands before validating either. A later demanded error
// takes precedence over an already invalid cardinality.
func (e *jsonpathExecutor) arithmeticOperand(n *jsonpathNode, input jsonpathItem, s jsonpathScope) (jsonpathItem, int, error) {
	var first jsonpathItem
	count := 0
	add := func(v jsonpathItem) {
		if count == 0 {
			first = v
		}
		if count < 2 {
			count++
		}
	}
	_, err := e.walk(n, input, s, func(v jsonpathItem) (bool, error) {
		if !e.strict && v.itemKind() == jsonbArray {
			for i := range v.items {
				if err := e.ctx.Err(); err != nil {
					return false, err
				}
				add(jsonpathItem{jsonbNode: &v.items[i]})
			}
		} else {
			add(v)
		}
		return true, nil
	})
	return first, count, err
}

func jsonpathNumericResult(v Value) jsonpathItem {
	return jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbNumber, number: jsonbDecimal{
		digits: v.text, exponent: v.exponent, scale: v.scale, negative: v.negative,
	}}}
}

func (e *jsonpathExecutor) arithmetic(n *jsonpathNode, input jsonpathItem, s jsonpathScope, emit func(jsonpathItem) (bool, error)) (bool, error) {
	if n.kind == "unary+" || n.kind == "unary-" {
		// Materialization is intentional: operand evaluation errors must precede
		// unary item validation/emission, even when silent keeps a result prefix.
		items, err := e.operand(n.left, s, !e.strict)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if err := e.ctx.Err(); err != nil {
				return false, err
			}
			if item.itemKind() != jsonbNumber {
				return false, jsonpathStructure("2203B", "operand of unary jsonpath operator "+n.kind[len("unary"):]+" is not a numeric value")
			}
			if n.kind == "unary-" {
				item = jsonpathNumericResult(numericNegate(jsonpathNumericOperand(item.number)))
			}
			if stopped, err := e.walk(n.next, item, s, emit); stopped || err != nil {
				return stopped, err
			}
		}
		return false, e.ctx.Err()
	}
	left, leftCount, err := e.arithmeticOperand(n.left, input, s)
	if err != nil {
		return false, err
	}
	right, rightCount, err := e.arithmeticOperand(n.right, input, s)
	if err != nil {
		return false, err
	}
	if leftCount != 1 || left.itemKind() != jsonbNumber {
		return false, jsonpathStructure("22038", "left operand of jsonpath operator "+n.kind+" is not a single numeric value")
	}
	if rightCount != 1 || right.itemKind() != jsonbNumber {
		return false, jsonpathStructure("22038", "right operand of jsonpath operator "+n.kind+" is not a single numeric value")
	}
	var op ArithmeticOperator
	switch n.kind {
	case "+":
		op = ArithmeticAdd
	case "-":
		op = ArithmeticSubtract
	case "*":
		op = ArithmeticMultiply
	case "/":
		op = ArithmeticDivide
	case "%":
		op = ArithmeticRemainder
	}
	result, err := numericArithmetic(e.ctx, op, jsonpathNumericOperand(left.number), jsonpathNumericOperand(right.number))
	if err != nil {
		// Only errors from this numeric operation belong to path suppression.
		// Operand, continuation and context failures keep their original channel.
		if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "22003" || diagnostic.Code == "22012") {
			return false, &jsonpathStructuralError{diagnostic: err}
		}
		return false, err
	}
	return e.walk(n.next, jsonpathNumericResult(result), s, emit)
}
