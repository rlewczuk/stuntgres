package xml

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Match syntax belongs to libxslt's pattern compiler, whose generic errors do
// not enter PostgreSQL's structured XML accumulator. Only balanced predicate
// bodies are passed to the XPath compiler and retain its real callbacks.
// Keeping those stages separate also avoids treating arbitrary XPath as a match.
type xsltPatternParser struct {
	p           parser
	s           string
	at          int
	namespaces  map[string]string
	key, number bool
}

func parseXSLTPattern(ctx context.Context, s string, ns map[string]string, key, number bool) ([]xsltPattern, error) {
	p := xsltPatternParser{p: parser{ctx: ctx}, s: s, namespaces: ns, key: key, number: number}
	var out []xsltPattern
	for p.p.err == nil {
		branch := p.branch()
		if p.p.err != nil {
			break
		}
		out = checkedAppend(&p.p, out, branch)
		if !p.take("|") {
			if p.at != len(s) {
				p.fail()
			}
			break
		}
	}
	if p.p.err != nil {
		return nil, p.p.err
	}
	return out, nil
}

func (p *xsltPatternParser) fail() {
	if p.p.err == nil {
		p.p.err = xsltFailure(XSLTCompile, "")
	}
}

func (p *xsltPatternParser) blank() {
	for p.at < len(p.s) && space(p.s[p.at]) && p.p.err == nil {
		p.p.err = p.p.ctx.Err()
		p.at++
	}
}

func (p *xsltPatternParser) take(s string) bool {
	p.blank()
	if p.p.err != nil || !strings.HasPrefix(p.s[p.at:], s) {
		return false
	}
	p.at += len(s)
	return true
}

func (p *xsltPatternParser) name() string {
	p.blank()
	start := p.at
	for p.at < len(p.s) && p.p.err == nil {
		p.p.err = p.p.ctx.Err()
		r, width := utf8.DecodeRuneInString(p.s[p.at:])
		if r == ':' || p.at == start && !nameStart(r) || p.at != start && !nameChar(r) {
			break
		}
		p.at += width
	}
	return p.s[start:p.at]
}

func (p *xsltPatternParser) literal() string {
	p.blank()
	if p.at == len(p.s) || p.s[p.at] != '\'' && p.s[p.at] != '"' {
		p.fail()
		return ""
	}
	quote := p.s[p.at]
	p.at++
	start := p.at
	for p.at < len(p.s) && p.s[p.at] != quote && p.p.err == nil {
		p.p.err = p.p.ctx.Err()
		p.at++
	}
	value := p.s[start:p.at]
	if p.at == len(p.s) {
		p.fail()
	} else {
		p.at++
	}
	return value
}

func (p *xsltPatternParser) branch() xsltPattern {
	p.p.err = p.p.ctx.Err()
	pattern := xsltPattern{namespaces: p.namespaces, priority: 0.5}
	deep := p.take("//")
	if !deep && p.take("/") {
		pattern.anchored = true
		p.blank()
		if p.at == len(p.s) || p.s[p.at] == '|' {
			pattern.root = true
			return pattern
		}
	}
	name := p.name()
	if !deep && !pattern.anchored && (name == "id" || name == "key") && p.take("(") {
		pattern.head = &xpathExpr{op: "call", name: name, args: []*xpathExpr{{op: "string", name: p.literal()}}}
		if name == "key" {
			if p.key || !p.take(",") {
				p.fail()
			}
			pattern.head.args = checkedAppend(&p.p, pattern.head.args, &xpathExpr{op: "string", name: p.literal()})
		}
		if !p.take(")") {
			p.fail()
		}
		deep = p.take("//")
		if !deep && !p.take("/") {
			return pattern
		}
		name = p.name()
	}
	for p.p.err == nil {
		step := p.step(name)
		pattern.steps = checkedAppend(&p.p, pattern.steps, xsltPatternStep{test: step, descendant: deep})
		deep = p.take("//")
		if !deep && !p.take("/") {
			break
		}
		name = p.name()
	}
	if len(pattern.steps) == 1 && !pattern.anchored && pattern.head == nil && !pattern.steps[0].descendant && len(pattern.steps[0].test.predicates) == 0 {
		step := pattern.steps[0].test
		switch step.test {
		case "name":
			_, pattern.priority, _ = xsltNameTest(step.name, p.namespaces)
		case "pi-target":
			pattern.priority = 0
		default:
			pattern.priority = -0.5
		}
	}
	return pattern
}

func (p *xsltPatternParser) step(name string) xpathStep {
	step := xpathStep{axis: "child", test: "name"}
	if name == "" && p.take("@") {
		step.axis = "attribute"
		name = p.name()
	} else if name != "" && p.take("::") {
		if name != "child" && name != "attribute" {
			p.fail()
		}
		step.axis = name
		name = p.name()
	}
	if name == "" {
		if !p.take("*") {
			p.fail()
		}
		step.name = "*"
	} else if p.take(":") {
		local := p.name()
		if local == "" {
			if !p.take("*") {
				p.fail()
			}
			local = "*"
		}
		step.name = name + ":" + local
	} else if p.take("(") {
		step.test = name
		switch name {
		case "node", "text", "comment":
		case "processing-instruction":
			p.blank()
			if p.at < len(p.s) && (p.s[p.at] == '\'' || p.s[p.at] == '"') {
				step.test, step.name = "pi-target", p.literal()
			}
		default:
			p.fail()
		}
		if !p.take(")") {
			p.fail()
		}
	} else {
		step.name = name
	}
	if step.test == "name" {
		if _, _, ok := xsltNameTest(step.name, p.namespaces); !ok {
			p.fail()
		}
	}
	for p.p.err == nil && p.take("[") {
		start, depth, quote := p.at, 1, byte(0)
		for p.at < len(p.s) && p.p.err == nil {
			p.p.err = p.p.ctx.Err()
			ch := p.s[p.at]
			if quote != 0 {
				if ch == quote {
					quote = 0
				}
			} else if ch == '\'' || ch == '"' {
				quote = ch
			} else if ch == '[' {
				depth++
			} else if ch == ']' {
				depth--
				if depth == 0 {
					break
				}
			}
			p.at++
		}
		if p.p.err != nil {
			break
		}
		if depth != 0 {
			p.fail()
			break
		}
		expression, err := compileXPathConfigured(p.p.ctx, p.s[start:p.at], !p.number)
		if err == nil {
			err = xsltCheckExpression(p.p.ctx, expression, false, !p.number, p.key)
		}
		if err != nil {
			p.p.err = err
			break
		}
		step.predicates = checkedAppend(&p.p, step.predicates, expression)
		p.at++
	}
	return step
}
