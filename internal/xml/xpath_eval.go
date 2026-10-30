package xml

import (
	"math"
	"strings"
)

type xpathValueKind uint8

const (
	xpathNodes xpathValueKind = iota
	xpathNumber
	xpathString
	xpathBoolean
)

type xpathValue struct {
	kind    xpathValueKind
	nodes   []*xpathNode
	number  float64
	text    string
	boolean bool
}

func xpathNum(n float64) xpathValue { return xpathValue{kind: xpathNumber, number: n} }
func xpathStr(s string) xpathValue  { return xpathValue{kind: xpathString, text: s} }
func xpathBool(b bool) xpathValue   { return xpathValue{kind: xpathBoolean, boolean: b} }
func (f *xpathFrame) runtimeError(detail string) {
	if f.p.err == nil {
		f.p.err = &XPathError{Stage: "evaluate", Detail: detail}
	}
}
func (f *xpathFrame) boolean(v xpathValue) bool {
	switch v.kind {
	case xpathBoolean:
		return v.boolean
	case xpathNodes:
		return len(v.nodes) != 0
	case xpathNumber:
		return v.number != 0 && !math.IsNaN(v.number)
	default:
		return v.text != ""
	}
}
func (f *xpathFrame) string(v xpathValue) string {
	switch v.kind {
	case xpathString:
		return v.text
	case xpathBoolean:
		if v.boolean {
			return "true"
		}
		return "false"
	case xpathNodes:
		if len(v.nodes) == 0 {
			return ""
		}
		return f.stringValue(v.nodes[0])
	default:
		return xpathFormatNumber(v.number)
	}
}
func (f *xpathFrame) number(v xpathValue) float64 {
	switch v.kind {
	case xpathNumber:
		return v.number
	case xpathBoolean:
		if v.boolean {
			return 1
		}
		return 0
	}
	number, err := xpathParseNumber(f.p.ctx, f.string(v))
	if err != nil {
		f.p.err = err
	}
	return number
}
func (f *xpathFrame) compare(a, b xpathValue, op string) bool {
	equality := op == "=" || op == "!="
	numberCompare := func(x, y float64) bool {
		switch op {
		case "=":
			return x == y
		case "!=":
			return x != y
		case "<":
			return x < y
		case ">":
			return x > y
		case "<=":
			return x <= y
		default:
			return x >= y
		}
	}
	if a.kind == xpathNodes || b.kind == xpathNodes {
		if equality && (a.kind == xpathBoolean || b.kind == xpathBoolean) {
			x, y := f.boolean(a), f.boolean(b)
			if op == "=" {
				return x == y
			}
			return x != y
		}
		if a.kind != xpathNodes {
			a, b = b, a
			switch op {
			case "<":
				op = ">"
			case ">":
				op = "<"
			case "<=":
				op = ">="
			case ">=":
				op = "<="
			}
		}
		if b.kind == xpathNodes {
			for _, x := range a.nodes {
				if !f.poll() {
					return false
				}
				sx := f.stringValue(x)
				for _, y := range b.nodes {
					if !f.poll() {
						return false
					}
					sy := f.stringValue(y)
					if equality {
						equal := f.p.namespaceEqual(sx, sy)
						if !f.poll() {
							return false
						}
						if equal == (op == "=") {
							return true
						}
					} else if numberCompare(f.number(xpathStr(sx)), f.number(xpathStr(sy))) {
						return true
					}
				}
			}
			return false
		}
		if len(a.nodes) == 0 {
			return false
		}
		lexical := equality && b.kind == xpathString
		numericB := 0.0
		if !lexical {
			numericB = f.number(b)
		}
		for _, x := range a.nodes {
			if !f.poll() {
				return false
			}
			s := f.stringValue(x)
			if lexical {
				equal := f.p.namespaceEqual(s, b.text)
				if !f.poll() {
					return false
				}
				if equal == (op == "=") {
					return true
				}
			} else if numberCompare(f.number(xpathStr(s)), numericB) {
				return true
			}
		}
		return false
	}
	if !equality {
		return numberCompare(f.number(a), f.number(b))
	}
	if a.kind == xpathBoolean || b.kind == xpathBoolean {
		if op == "=" {
			return f.boolean(a) == f.boolean(b)
		}
		return f.boolean(a) != f.boolean(b)
	}
	if a.kind == xpathNumber || b.kind == xpathNumber {
		return numberCompare(f.number(a), f.number(b))
	}
	equal := f.p.namespaceEqual(f.string(a), f.string(b))
	if !f.poll() {
		return false
	}
	return equal == (op == "=")
}
func (f *xpathFrame) eval(e *xpathExpr) xpathValue {
	if !f.poll() {
		return xpathValue{}
	}
	f.depth++
	defer func() { f.depth-- }()
	if f.depth > 5000 {
		f.runtimeError("Recursion limit exceeded")
		return xpathValue{}
	}
	switch e.op {
	case "string":
		return xpathStr(e.name)
	case "number":
		return xpathNum(e.number)
	case "variable":
		prefix, _, qualified := strings.Cut(e.name, ":")
		_, resolved := f.namespaceURI(prefix)
		if qualified && !resolved {
			f.runtimeError("")
		} else {
			f.runtimeError("Undefined variable")
		}
		return xpathValue{}
	case "call":
		return f.call(e)
	case "negate":
		return xpathNum(-f.number(f.eval(e.args[0])))
	case "filter":
		v := f.eval(e.args[0])
		if v.kind != xpathNodes {
			f.runtimeError("Invalid type")
			return xpathValue{}
		}
		return xpathValue{nodes: f.filter(v.nodes, e.predicates)}
	case "path":
		nodes := []*xpathNode{f.node}
		if e.absolute {
			nodes[0] = f.tree.root
		} else if len(e.args) != 0 {
			v := f.eval(e.args[0])
			if v.kind != xpathNodes {
				f.runtimeError("Invalid type")
				return xpathValue{}
			}
			nodes = v.nodes
		}
		for _, s := range e.steps {
			if !f.poll() {
				break
			}
			if s.test == "name" {
				prefix, _, qualified := strings.Cut(s.name, ":")
				_, resolved := f.namespaceURI(prefix)
				if qualified && !resolved {
					f.runtimeError("Undefined namespace prefix")
					break
				}
			}
			var next []*xpathNode
			for _, n := range nodes {
				if !f.poll() {
					break
				}
				var selected []*xpathNode
				for _, candidate := range f.axis(n, s.axis) {
					if !f.poll() {
						break
					}
					if f.matches(candidate, s) {
						selected = checkedAppend(&f.p, selected, candidate)
					}
				}
				selected = f.filter(selected, s.predicates)
				for _, candidate := range selected {
					next = checkedAppend(&f.p, next, candidate)
				}
			}
			nodes = f.normalize(next)
		}
		return xpathValue{nodes: nodes}
	}
	a := f.eval(e.args[0])
	if !f.poll() {
		return xpathValue{}
	}
	if e.op == "and" && !f.boolean(a) {
		return xpathBool(false)
	}
	if e.op == "or" && f.boolean(a) {
		return xpathBool(true)
	}
	b := f.eval(e.args[1])
	if !f.poll() {
		return xpathValue{}
	}
	switch e.op {
	case "and":
		return xpathBool(f.boolean(b))
	case "or":
		return xpathBool(f.boolean(b))
	case "=", "!=", "<", ">", "<=", ">=":
		return xpathBool(f.compare(a, b, e.op))
	case "|":
		if a.kind != xpathNodes || b.kind != xpathNodes {
			f.runtimeError("Invalid type")
			return xpathValue{}
		}
		var nodes []*xpathNode
		for _, set := range [][]*xpathNode{a.nodes, b.nodes} {
			for _, n := range set {
				if !f.poll() {
					return xpathValue{}
				}
				nodes = checkedAppend(&f.p, nodes, n)
			}
		}
		return xpathValue{nodes: f.normalize(nodes)}
	case "+":
		return xpathNum(f.number(a) + f.number(b))
	case "-":
		return xpathNum(f.number(a) - f.number(b))
	case "*":
		return xpathNum(f.number(a) * f.number(b))
	case "div":
		return xpathNum(f.number(a) / f.number(b))
	case "mod":
		return xpathNum(math.Mod(f.number(a), f.number(b)))
	}
	f.runtimeError("Invalid expression")
	return xpathValue{}
}
func (f *xpathFrame) filter(nodes []*xpathNode, predicates []*xpathExpr) []*xpathNode {
	oldNode, oldPosition, oldSize := f.node, f.position, f.size
	defer func() { f.node, f.position, f.size = oldNode, oldPosition, oldSize }()
	for _, predicate := range predicates {
		var out []*xpathNode
		f.size = len(nodes)
		for i, n := range nodes {
			if !f.poll() {
				return nil
			}
			f.node = n
			f.position = i + 1
			v := f.eval(predicate)
			keep := f.boolean(v)
			if v.kind == xpathNumber {
				keep = v.number == float64(i+1)
			}
			if keep {
				out = checkedAppend(&f.p, out, n)
			}
		}
		nodes = out
	}
	return nodes
}
