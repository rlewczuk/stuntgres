package sqltype

import "strings"

type jsonpathTruth uint8

const (
	jsonpathFalse jsonpathTruth = iota
	jsonpathTrue
	jsonpathUnknown
)

func jsonpathBoolean(b bool) jsonpathTruth {
	if b {
		return jsonpathTrue
	}
	return jsonpathFalse
}

func (e *jsonpathExecutor) condition(n *jsonpathNode, s jsonpathScope) (jsonpathTruth, error) {
	truth, err := e.conditionValue(n, s)
	if _, ok := err.(*jsonpathStructuralError); ok {
		return jsonpathUnknown, nil
	}
	return truth, err
}
func (e *jsonpathExecutor) conditionValue(n *jsonpathNode, s jsonpathScope) (jsonpathTruth, error) {
	if err := e.ctx.Err(); err != nil {
		return jsonpathUnknown, err
	}
	switch n.kind {
	case "!", "is unknown":
		a, err := e.condition(n.left, s)
		if err != nil {
			return jsonpathUnknown, err
		}
		if n.kind == "is unknown" {
			return jsonpathBoolean(a == jsonpathUnknown), nil
		}
		if a == jsonpathUnknown {
			return a, nil
		}
		return jsonpathBoolean(a == jsonpathFalse), nil
	case "&&", "||":
		a, err := e.condition(n.left, s)
		if err != nil {
			return jsonpathUnknown, err
		}
		if n.kind == "&&" && a == jsonpathFalse || n.kind == "||" && a == jsonpathTrue {
			return a, nil
		}
		b, err := e.condition(n.right, s)
		if err != nil {
			return jsonpathUnknown, err
		}
		if n.kind == "&&" {
			if b == jsonpathFalse {
				return b, nil
			}
			if a == jsonpathUnknown || b == jsonpathUnknown {
				return jsonpathUnknown, nil
			}
			return jsonpathTrue, nil
		}
		if b == jsonpathTrue {
			return b, nil
		}
		if a == jsonpathUnknown || b == jsonpathUnknown {
			return jsonpathUnknown, nil
		}
		return jsonpathFalse, nil
	case "exists":
		found := false
		_, err := e.walk(n.left, e.root, s, func(*jsonbNode) (bool, error) { found = true; return e.strict, nil })
		return jsonpathBoolean(found), err
	}
	left, err := e.operand(n.left, s, !e.strict)
	if err != nil {
		return jsonpathUnknown, err
	}
	if n.kind == "like_regex" {
		result := jsonpathFalse
		for _, a := range left {
			truth := jsonpathUnknown
			if a.kind == jsonbString {
				matched, err := e.match(n, a.text)
				if err != nil {
					return jsonpathUnknown, err
				}
				truth = jsonpathBoolean(matched)
			}
			if truth == jsonpathUnknown {
				if e.strict {
					return truth, nil
				}
				result = truth
			} else if truth == jsonpathTrue {
				if !e.strict {
					return truth, nil
				}
				result = truth
			}
		}
		return result, nil
	}
	right, err := e.operand(n.right, s, !e.strict && n.kind != "starts with")
	if err != nil {
		return jsonpathUnknown, err
	}
	result := jsonpathFalse
	unknown := false
	for _, a := range left {
		for _, b := range right {
			if err := e.ctx.Err(); err != nil {
				return jsonpathUnknown, err
			}
			truth, err := e.pair(n.kind, a, b)
			if err != nil {
				return jsonpathUnknown, err
			}
			if truth == jsonpathUnknown {
				if e.strict {
					return truth, nil
				}
				unknown = true
			}
			if truth == jsonpathTrue {
				if !e.strict {
					return truth, nil
				}
				result = truth
			}
		}
	}
	if result != jsonpathTrue && unknown {
		result = jsonpathUnknown
	}
	return result, nil
}
func (e *jsonpathExecutor) operand(n *jsonpathNode, s jsonpathScope, unwrap bool) ([]*jsonbNode, error) {
	var nodes []*jsonbNode
	_, err := e.walk(n, e.root, s, func(v *jsonbNode) (bool, error) {
		if unwrap && v.kind == jsonbArray {
			for i := range v.items {
				if err := e.ctx.Err(); err != nil {
					return false, err
				}
				nodes = append(nodes, &v.items[i])
			}
		} else {
			nodes = append(nodes, v)
		}
		return true, nil
	})
	return nodes, err
}

func (e *jsonpathExecutor) pair(op string, a, b *jsonbNode) (jsonpathTruth, error) {
	if op == "starts with" {
		if a.kind != jsonbString || b.kind != jsonbString {
			return jsonpathUnknown, nil
		}
		if len(a.text) < len(b.text) {
			return jsonpathFalse, nil
		}
		c, err := e.stringCompare(a.text[:len(b.text)], b.text)
		return jsonpathBoolean(c == 0), err
	}
	if a.kind == jsonbNull || b.kind == jsonbNull {
		if a.kind != b.kind {
			return jsonpathBoolean(op == "!="), nil
		}
		return jsonpathBoolean(op == "==" || op == "<=" || op == ">="), nil
	}
	if a.kind != b.kind || a.kind == jsonbArray || a.kind == jsonbObject {
		return jsonpathUnknown, nil
	}
	c := 0
	var err error
	switch a.kind {
	case jsonbString:
		c, err = e.stringCompare(a.text, b.text)
	case jsonbBoolean:
		if a.boolean != b.boolean {
			c = -1
			if a.boolean {
				c = 1
			}
		}
	case jsonbNumber:
		c, err = e.numberCompare(a.number, b.number)
	}
	if err != nil {
		return jsonpathUnknown, err
	}
	switch op {
	case "==":
		return jsonpathBoolean(c == 0), nil
	case "!=":
		return jsonpathBoolean(c != 0), nil
	case "<":
		return jsonpathBoolean(c < 0), nil
	case ">":
		return jsonpathBoolean(c > 0), nil
	case "<=":
		return jsonpathBoolean(c <= 0), nil
	case ">=":
		return jsonpathBoolean(c >= 0), nil
	}
	return jsonpathUnknown, nil
}
func (e *jsonpathExecutor) stringCompare(a, b string) (int, error) {
	length := min(len(a), len(b))
	for start := 0; start < length; start += jsonbWorkChunk {
		if err := e.ctx.Err(); err != nil {
			return 0, err
		}
		end := min(start+jsonbWorkChunk, length)
		if c := strings.Compare(a[start:end], b[start:end]); c != 0 {
			return c, nil
		}
	}
	return len(a) - len(b), e.ctx.Err()
}
func (e *jsonpathExecutor) numberCompare(a, b jsonbDecimal) (int, error) {
	av, bv := a.view(), b.view()
	// Magnitude/sign comparison is constant-time; only equal-width coefficients
	// require a potentially long scan. Preserve exact decimal ordering and poll it.
	if av.negative != bv.negative || av.length() == 0 || bv.length() == 0 || int64(av.length())+av.exponent != int64(bv.length())+bv.exponent {
		return compareNumericViews(av, bv), e.ctx.Err()
	}
	for i := range max(av.length(), bv.length()) {
		if i%jsonbWorkChunk == 0 {
			if err := e.ctx.Err(); err != nil {
				return 0, err
			}
		}
		ac, bc := av.digit(i), bv.digit(i)
		if ac != bc {
			c := -1
			if ac > bc {
				c = 1
			}
			if av.negative {
				c = -c
			}
			return c, nil
		}
	}
	return 0, e.ctx.Err()
}
