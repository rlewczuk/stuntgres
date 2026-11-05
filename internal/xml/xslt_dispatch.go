package xml

import "context"

type xsltDispatchKey struct {
	mode string
	kind xpathNodeKind
	name string
}
type xsltTemplateBranch struct {
	template *xsltTemplate
	pattern  *xsltPattern
}

func (p *xsltProgram) indexTemplates(ctx context.Context) error {
	check := parser{ctx: ctx}
	p.dispatch = make(map[xsltDispatchKey][]xsltTemplateBranch)
	for _, t := range p.templates {
		for at := range t.patterns {
			if err := ctx.Err(); err != nil {
				return err
			}
			pattern := &t.patterns[at]
			kinds := []xpathNodeKind{xpathRoot, xpathElement, xpathAttribute, xpathNamespace, xpathText, xpathComment, xpathPI}
			name := "*"
			if pattern.root {
				kinds = []xpathNodeKind{xpathRoot}
			} else if len(pattern.steps) > 0 {
				step := pattern.steps[len(pattern.steps)-1].test
				switch step.test {
				case "name":
					kind := xpathElement
					if step.axis == "attribute" {
						kind = xpathAttribute
					}
					kinds = []xpathNodeKind{kind}
					expanded, _, ok := xsltNameTest(step.name, pattern.namespaces)
					if ok && expanded.URI != "*" {
						name = expanded.key()
					}
				case "text":
					kinds = []xpathNodeKind{xpathText}
				case "comment":
					kinds = []xpathNodeKind{xpathComment}
				case "processing-instruction":
					kinds = []xpathNodeKind{xpathPI}
				case "pi-target":
					kinds = []xpathNodeKind{xpathPI}
					name = "\x00" + step.name
				}
			}
			for _, kind := range kinds {
				key := xsltDispatchKey{t.mode, kind, name}
				p.dispatch[key] = checkedAppend(&check, p.dispatch[key], xsltTemplateBranch{t, pattern})
				if check.err != nil {
					return check.err
				}
			}
		}
	}
	return ctx.Err()
}
func (r *xsltRuntime) templateCandidates(c xsltContext) [3][]xsltTemplateBranch {
	name := c.node.uri + "\x00" + c.node.local
	return [3][]xsltTemplateBranch{
		r.program.dispatch[xsltDispatchKey{c.mode, c.node.kind, name}],
		r.program.dispatch[xsltDispatchKey{c.mode, c.node.kind, c.node.uri + "\x00*"}],
		r.program.dispatch[xsltDispatchKey{c.mode, c.node.kind, "*"}],
	}
}
