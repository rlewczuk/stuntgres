package xml

import "context"

type xsltPatternStep struct {
	test       xpathStep
	descendant bool
}
type xsltPattern struct {
	root       bool
	anchored   bool
	steps      []xsltPatternStep
	head       *xpathExpr
	namespaces map[string]string
	priority   float64
}

func compileXSLTPattern(ctx context.Context, s string, ns map[string]string, keyDeclaration bool) ([]xsltPattern, error) {
	return compileXSLTPatternMode(ctx, s, ns, keyDeclaration, false)
}
func compileXSLTNumberPattern(ctx context.Context, s string, ns map[string]string) ([]xsltPattern, error) {
	return compileXSLTPatternMode(ctx, s, ns, false, true)
}
func compileXSLTPatternMode(ctx context.Context, s string, ns map[string]string, keyDeclaration, number bool) ([]xsltPattern, error) {
	return parseXSLTPattern(ctx, s, ns, keyDeclaration, number)
}
func (p *xsltPattern) matches(f *xpathFrame, n *xpathNode) bool {
	oldBindings, oldNode, oldPosition, oldSize := f.bindings, f.node, f.position, f.size
	f.bindings = p.namespaces
	defer func() { f.bindings, f.node, f.position, f.size = oldBindings, oldNode, oldPosition, oldSize }()
	if p.root {
		return n.kind == xpathRoot
	}
	type state struct {
		node *xpathNode
		step int
	}
	stack := []state{{n, len(p.steps) - 1}}
	for len(stack) > 0 && f.poll() {
		last := len(stack) - 1
		s := stack[last]
		stack = stack[:last]
		if s.step < 0 {
			if p.head != nil {
				f.node = s.node
				f.position, f.size = 1, 1
				v := f.eval(p.head)
				for _, candidate := range v.nodes {
					if candidate == s.node {
						return true
					}
				}
			} else if !p.anchored || s.node != nil && s.node.kind == xpathRoot {
				return true
			}
			continue
		}
		if s.node == nil {
			continue
		}
		step := p.steps[s.step]
		if !f.matches(s.node, step.test) {
			continue
		}
		if len(step.test.predicates) > 0 {
			if s.node.parent == nil {
				continue
			}
			var siblings []*xpathNode
			for _, candidate := range f.axis(s.node.parent, step.test.axis) {
				if f.matches(candidate, step.test) {
					siblings = checkedAppend(&f.p, siblings, candidate)
				}
			}
			selected := f.filter(siblings, step.test.predicates)
			found := false
			for _, candidate := range selected {
				if candidate == s.node {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		parent := s.node.parent
		if s.step == 0 && !p.anchored && p.head == nil {
			return true
		}
		if step.descendant {
			for ancestor := parent; ancestor != nil && f.poll(); ancestor = ancestor.parent {
				stack = checkedAppend(&f.p, stack, state{ancestor, s.step - 1})
			}
		} else {
			stack = checkedAppend(&f.p, stack, state{parent, s.step - 1})
		}
	}
	return false
}
