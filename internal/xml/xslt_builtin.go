package xml

// xsltFocus is the invocation's ambient XPath list context. Instruction XPath
// evaluations borrow their explicit xsltContext instead of mutating this state.
type xsltFocus struct {
	position, size int
}

// builtinChild advances xsltDefaultProcessOneNode's child traversal. Projected
// text retains its original source kind: CDATA dispatch inherits the ambient
// focus, whereas matched ordinary text/comment/PI templates update it. Elements
// update it even when they invoke another default rule. Unmatched leaves do not.
// The continuation is resumed only after that child's template has completed;
// nested default traversals restore the parent focus before the next sibling.
func (r *xsltRuntime) builtinChild(task *xsltTask) (xsltContext, *xsltTemplate, bool) {
	c := task.context
	if task.index >= len(c.node.children) || !r.frame.poll() {
		return xsltContext{}, nil, false
	}
	position := task.index + 1
	c.node = c.node.children[task.index]
	task.index++
	c.depth++
	t := r.selectTemplate(c, false)
	update := c.node.kind == xpathRoot || c.node.kind == xpathElement
	if t != nil {
		switch c.node.kind {
		case xpathText:
			update = c.node.source == nil || c.node.source.kind != cdataNode
		case xpathComment, xpathPI:
			update = true
		}
	}
	if update {
		r.focus = xsltFocus{position: position, size: len(task.context.node.children)}
	}
	c.position, c.size = r.focus.position, r.focus.size
	return c, t, true
}
