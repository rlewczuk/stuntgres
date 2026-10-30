package xml

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Compiled expressions contain only owned query spelling and immutable syntax.
// Evaluation frames and document nodes are exclusively operation-owned.
type xpathExpr struct {
	op, name   string
	number     float64
	args       []*xpathExpr
	steps      []xpathStep
	predicates []*xpathExpr
	absolute   bool
}
type xpathStep struct {
	axis, test, name string
	predicates       []*xpathExpr
}
type xpathToken struct{ kind, text string }
type xpathParser struct {
	ctx      context.Context
	s        string
	i, depth int
	token    xpathToken
	err      error
}
type XPathError struct{ Stage, Detail string }

func (e *XPathError) Error() string {
	switch e.Stage {
	case "empty":
		return "empty XPath expression"
	case "row-empty":
		return "row path filter must not be empty string"
	case "column-empty":
		return "column path filter must not be empty string"
	case "compile":
		return "invalid XPath expression"
	default:
		return "could not create XPath object"
	}
}
func compileXPath(ctx context.Context, s string) (*xpathExpr, error) {
	p := xpathParser{ctx: ctx, s: s}
	p.next()
	e := p.expression(1)
	if p.err == nil && p.token.kind != "eof" {
		p.fail()
	}
	return e, p.err
}
func (p *xpathParser) fail() {
	if p.err == nil {
		p.err = &XPathError{Stage: "compile", Detail: "Invalid expression"}
	}
}
func (p *xpathParser) next() {
	if p.err != nil {
		return
	}
	p.err = p.ctx.Err()
	if p.err != nil {
		return
	}
	for p.i < len(p.s) && space(p.s[p.i]) {
		if p.i&4095 == 0 {
			p.err = p.ctx.Err()
			if p.err != nil {
				return
			}
		}
		p.i++
	}
	if p.i == len(p.s) {
		p.token = xpathToken{kind: "eof"}
		return
	}
	start := p.i
	c := p.s[p.i]
	p.i++
	if c == '\'' || c == '"' {
		start = p.i
		for p.i < len(p.s) && p.s[p.i] != c {
			if p.i&4095 == 0 {
				p.err = p.ctx.Err()
				if p.err != nil {
					return
				}
			}
			p.i++
		}
		if p.i == len(p.s) {
			p.fail()
			return
		}
		p.token = xpathToken{"literal", p.s[start:p.i]}
		p.i++
		return
	}
	if c >= '0' && c <= '9' || c == '.' && p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			if p.i&4095 == 0 {
				p.err = p.ctx.Err()
				if p.err != nil {
					return
				}
			}
			p.i++
		}
		if c != '.' && p.i < len(p.s) && p.s[p.i] == '.' {
			p.i++
			for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
				if p.i&4095 == 0 {
					p.err = p.ctx.Err()
					if p.err != nil {
						return
					}
				}
				p.i++
			}
		}
		if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
			p.i++
			if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
				p.i++
			}
			for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
				if p.i&4095 == 0 {
					p.err = p.ctx.Err()
					if p.err != nil {
						return
					}
				}
				p.i++
			}
		}
		p.token = xpathToken{"number", p.s[start:p.i]}
		return
	}
	r, w := utf8.DecodeRuneInString(p.s[start:])
	if r != ':' && nameStart(r) {
		p.i = start + w
		scan := func() {
			for p.i < len(p.s) {
				p.err = p.ctx.Err()
				if p.err != nil {
					return
				}
				r, w := utf8.DecodeRuneInString(p.s[p.i:])
				if r == ':' || !nameChar(r) {
					break
				}
				p.i += w
			}
		}
		scan()
		if p.err != nil {
			return
		}
		if p.i < len(p.s) && p.s[p.i] == ':' && (p.i+1 == len(p.s) || p.s[p.i+1] != ':') {
			p.i++
			if p.i < len(p.s) && p.s[p.i] == '*' {
				p.i++
			} else {
				if p.i == len(p.s) {
					p.fail()
					return
				}
				r, w = utf8.DecodeRuneInString(p.s[p.i:])
				if r == ':' || !nameStart(r) {
					p.fail()
					return
				}
				p.i += w
				scan()
			}
		}
		p.token = xpathToken{"name", p.s[start:p.i]}
		return
	}
	if p.i < len(p.s) {
		pair := p.s[start : p.i+1]
		switch pair {
		case "//", "..", "::", "!=", "<=", ">=":
			p.i++
			p.token = xpathToken{pair, pair}
			return
		}
	}
	switch c {
	case '/', '.', '@', '[', ']', '(', ')', ',', '|', '+', '-', '*', '=', '<', '>', '$':
		p.token = xpathToken{string(c), string(c)}
	default:
		p.fail()
	}
}
func (p *xpathParser) take(k string) bool {
	if p.err != nil || p.token.kind != k {
		return false
	}
	p.next()
	return true
}
func (p *xpathParser) need(k string) {
	if !p.take(k) {
		p.fail()
	}
}
func xpathPrecedence(t xpathToken) int {
	switch t.kind {
	case "=", "!=":
		return 3
	case "<", ">", "<=", ">=":
		return 4
	case "+", "-":
		return 5
	case "*":
		return 6
	case "|":
		return 8
	case "name":
		switch t.text {
		case "or":
			return 1
		case "and":
			return 2
		case "div", "mod":
			return 6
		}
	}
	return 0
}
func (p *xpathParser) expression(minimum int) *xpathExpr {
	if p.err != nil {
		return nil
	}
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 5000 {
		p.err = &XPathError{Stage: "compile", Detail: "Recursion limit exceeded"}
		return nil
	}
	var left *xpathExpr
	if p.take("-") {
		left = &xpathExpr{op: "negate", args: []*xpathExpr{p.expression(7)}}
	} else {
		left = p.path()
	}
	for p.err == nil {
		prec := xpathPrecedence(p.token)
		if prec < minimum {
			break
		}
		op := p.token.text
		p.next()
		right := p.expression(prec + 1)
		left = &xpathExpr{op: op, args: []*xpathExpr{left, right}}
	}
	return left
}
func (p *xpathParser) stepStart() bool {
	switch p.token.kind {
	case ".", "..", "@", "*":
		return true
	case "name":
		return true
	}
	return false
}
func (p *xpathParser) path() *xpathExpr {
	if p.token.kind == "/" || p.token.kind == "//" {
		deep := p.token.kind == "//"
		p.next()
		e := &xpathExpr{op: "path", absolute: true}
		if deep {
			e.steps = append(e.steps, xpathStep{axis: "descendant-or-self", test: "node"})
		}
		if !p.stepStart() {
			if deep {
				p.fail()
			}
			return e
		}
		e.steps = append(e.steps, p.step())
		return p.pathTail(e)
	}
	// A name followed by '(' is a primary function unless it is a node test.
	if p.token.kind == "name" {
		name := p.token.text
		function := p.followedBy("(") && name != "node" && name != "text" && name != "comment" && name != "processing-instruction"
		if !function {
			return p.pathTail(&xpathExpr{op: "path", steps: []xpathStep{p.step()}})
		}
	} else if p.stepStart() {
		return p.pathTail(&xpathExpr{op: "path", steps: []xpathStep{p.step()}})
	}
	var e *xpathExpr
	switch p.token.kind {
	case "literal":
		e = &xpathExpr{op: "string", name: p.token.text}
		p.next()
	case "number":
		n, err := xpathParseNumber(p.ctx, p.token.text)
		if err != nil {
			p.err = err
			return nil
		}
		e = &xpathExpr{op: "number", number: n}
		p.next()
	case "$":
		p.next()
		if p.token.kind != "name" || strings.Contains(p.token.text, "*") {
			p.fail()
			return nil
		}
		e = &xpathExpr{op: "variable", name: p.token.text}
		p.next()
	case "(":
		p.next()
		e = p.expression(1)
		p.need(")")
	case "name":
		name := p.token.text
		if strings.Contains(name, "*") {
			p.fail()
			return nil
		}
		p.next()
		p.need("(")
		e = &xpathExpr{op: "call", name: name}
		if p.token.kind != ")" {
			for p.err == nil {
				e.args = append(e.args, p.expression(1))
				if !p.take(",") {
					break
				}
			}
		}
		p.need(")")
	default:
		p.fail()
		return nil
	}
	for p.take("[") {
		if e.op != "filter" {
			e = &xpathExpr{op: "filter", args: []*xpathExpr{e}}
		}
		e.predicates = append(e.predicates, p.expression(1))
		p.need("]")
	}
	if p.token.kind == "/" || p.token.kind == "//" {
		e = &xpathExpr{op: "path", args: []*xpathExpr{e}}
		return p.pathTail(e)
	}
	return e
}
func (p *xpathParser) pathTail(e *xpathExpr) *xpathExpr {
	for p.err == nil && (p.token.kind == "/" || p.token.kind == "//") {
		deep := p.token.kind == "//"
		p.next()
		if deep {
			e.steps = append(e.steps, xpathStep{axis: "descendant-or-self", test: "node"})
		}
		if !p.stepStart() {
			p.fail()
			break
		}
		e.steps = append(e.steps, p.step())
	}
	return e
}
func (p *xpathParser) step() xpathStep {
	s := xpathStep{axis: "child", test: "name"}
	if p.take(".") {
		s.axis = "self"
		s.test = "node"
		return s
	}
	if p.take("..") {
		s.axis = "parent"
		s.test = "node"
		return s
	}
	if p.take("@") {
		s.axis = "attribute"
	} else if p.token.kind == "name" && p.followedBy("::") {
		s.axis = p.token.text
		p.next()
		p.need("::")
		switch s.axis {
		case "ancestor", "ancestor-or-self", "attribute", "child", "descendant", "descendant-or-self", "following", "following-sibling", "namespace", "parent", "preceding", "preceding-sibling", "self":
		default:
			p.fail()
		}
	}
	if p.take("*") {
		s.name = "*"
	} else if p.token.kind == "name" {
		s.name = p.token.text
		p.next()
		if p.take("(") {
			s.test = s.name
			s.name = ""
			switch s.test {
			case "node", "text", "comment":
			case "processing-instruction":
				if p.token.kind == "literal" {
					s.name = p.token.text
					p.next()
					s.test = "pi-target"
				}
			default:
				p.fail()
			}
			p.need(")")
		}
	} else {
		p.fail()
	}
	for p.take("[") {
		s.predicates = append(s.predicates, p.expression(1))
		p.need("]")
	}
	return s
}

// Inspect punctuation without re-lexing a potentially long following token or
// restoring a snapshot that could discard a terminal context error.
func (p *xpathParser) followedBy(punctuation string) bool {
	i := p.i
	for i < len(p.s) && space(p.s[i]) {
		if i&4095 == 0 {
			p.err = p.ctx.Err()
			if p.err != nil {
				return false
			}
		}
		i++
	}
	return strings.HasPrefix(p.s[i:], punctuation)
}
