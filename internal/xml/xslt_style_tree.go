package xml

import "context"

// Compilation has two products: immutable instructions and the actual
// stylesheet document visible to document(”). The released compiler strips
// stylesheet-only nodes and splices xsl:text's real children; foreign top-level
// data is deliberately not preprocessed. No alternate parser/tree is created.
func (p *xsltProgram) preprocessStyleTree(ctx context.Context) error {
	check := parser{ctx: ctx}
	type visit struct {
		instruction *xsltInstruction
		finish      bool
	}
	stack := []visit{{instruction: p.root}}
	for len(stack) > 0 && check.err == nil {
		check.err = ctx.Err()
		if check.err != nil {
			break
		}
		last := len(stack) - 1
		v := stack[last]
		stack = stack[:last]
		i := v.instruction
		if i.source == nil || i.source.kind != elementNode {
			continue
		}
		if i != p.root && i.source.uri != xsltNamespaceURI && i.op == i.source.local {
			continue
		}
		if !v.finish {
			stack = checkedAppend(&check, stack, visit{i, true})
			for j := len(i.children) - 1; j >= 0; j-- {
				stack = checkedAppend(&check, stack, visit{instruction: i.children[j]})
			}
			continue
		}
		if i.op == "text" {
			if xsltAttribute(i, "disable-output-escaping") == "yes" {
				for _, n := range i.source.children {
					if n.kind == textNode {
						n.disableEscaping = true
					}
				}
			}
			continue
		}
		out := i.source.children[:0]
		for _, child := range i.children {
			if check.err = ctx.Err(); check.err != nil {
				break
			}
			if child.op == "text" && child.source.kind == elementNode {
				for _, text := range child.source.children {
					out = checkedAppend(&check, out, text)
				}
			} else {
				out = checkedAppend(&check, out, child.source)
			}
		}
		i.source.children = out
	}
	return check.err
}
