package xml

import (
	"strconv"
	"strings"
)

type xsltTarget struct {
	tree   *xsltResultTree
	parent *xsltResultNode
}

func (t xsltTarget) children() *[]*xsltResultNode {
	if t.parent != nil {
		return &t.parent.Children
	}
	return &t.tree.Children
}
func xsltTreeString(p *parser, tree *xsltResultTree) string {
	if tree == nil {
		return ""
	}
	var b strings.Builder
	stack := append([]*xsltResultNode(nil), tree.Children...)
	// Traverse in source order without recursion or mutable result sharing.
	for l, h := 0, len(stack)-1; l < h; l, h = l+1, h-1 {
		stack[l], stack[h] = stack[h], stack[l]
	}
	for len(stack) > 0 && p.err == nil {
		p.err = p.ctx.Err()
		last := len(stack) - 1
		n := stack[last]
		stack = stack[:last]
		if n.Kind == xsltResultText {
			p.appendValue(&b, n.Value)
		}
		for i := len(n.Children) - 1; i >= 0; i-- {
			stack = checkedAppend(p, stack, n.Children[i])
		}
	}
	return b.String()
}
func (r *xsltRuntime) appendResult(target xsltTarget, n *xsltResultNode) {
	if !r.frame.poll() {
		return
	}
	if n.Kind == xsltResultText && n.Value == "" {
		return
	}
	nodes := target.children()
	*nodes = checkedAppend(&r.frame.p, *nodes, n)
}
func (r *xsltRuntime) namespace(n *xsltResultNode, prefix, uri string) {
	if prefix == "xml" {
		return
	}
	for index, ns := range n.Namespaces {
		if !r.frame.poll() {
			return
		}
		if ns.Prefix == prefix {
			n.Namespaces[index].URI = uri
			return
		}
	}
	n.Namespaces = checkedAppend(&r.frame.p, n.Namespaces, xsltNamespace{prefix, uri})
}
func (r *xsltRuntime) fixName(n *xsltResultNode, name xsltName, attribute bool) xsltName {
	if name.URI == "" {
		name.Prefix = ""
		if !attribute {
			r.namespace(n, "", "")
		}
		return name
	}
	if name.URI == xmlNamespaceURI {
		name.Prefix = "xml"
		return name
	}
	if attribute && name.Prefix == "" {
		r.prefixCounter++
		name.Prefix = "ns_" + strconv.Itoa(r.prefixCounter)
	}
	for _, ns := range n.Namespaces {
		if ns.Prefix == name.Prefix && ns.URI != name.URI {
			r.prefixCounter++
			name.Prefix = "ns_" + strconv.Itoa(r.prefixCounter)
			break
		}
	}
	r.namespace(n, name.Prefix, name.URI)
	return name
}
func (r *xsltRuntime) attribute(target xsltTarget, name xsltName, value string) {
	if target.parent == nil {
		return
	}
	if len(target.parent.Children) > 0 {
		r.fail()
		return
	}
	if name.Local == "xmlns" && name.URI == "" || name.Prefix == "xmlns" {
		return
	}
	n := target.parent
	for index, a := range n.Attributes {
		if a.Name.key() == name.key() {
			n.Attributes[index].Value = value
			return
		}
	}
	name = r.fixName(n, name, true)
	n.Attributes = checkedAppend(&r.frame.p, n.Attributes, xsltResultAttribute{Name: name, Value: value})
}
func (r *xsltRuntime) alias(name xsltName) xsltName {
	if uri, ok := r.program.aliases[name.URI]; ok {
		name.URI = uri
		if uri == "" {
			name.Prefix = ""
		}
	}
	return name
}
func (r *xsltRuntime) shallowCopy(target xsltTarget, n *xpathNode) *xsltResultNode {
	switch n.kind {
	case xpathRoot:
		return nil
	case xpathElement:
		prefix, _, qualified := strings.Cut(n.name, ":")
		if !qualified {
			prefix = ""
		}
		out := &xsltResultNode{Kind: xsltResultElement, Name: xsltName{Prefix: prefix, Local: n.local, URI: n.uri}}
		for _, ns := range n.namespaces {
			if ns.local != "xml" {
				r.namespace(out, ns.local, ns.value)
			}
		}
		out.Name = r.fixName(out, out.Name, false)
		r.appendResult(target, out)
		return out
	case xpathAttribute:
		prefix, _, qualified := strings.Cut(n.name, ":")
		if !qualified {
			prefix = ""
		}
		r.attribute(target, xsltName{Prefix: prefix, Local: n.local, URI: n.uri}, n.value)
	case xpathNamespace:
		if target.parent != nil && len(target.parent.Children) == 0 {
			r.namespace(target.parent, n.local, n.value)
		}
	case xpathText:
		doe := n.source != nil && n.source.disableEscaping
		r.appendResult(target, &xsltResultNode{Kind: xsltResultText, Value: n.value, DisableEscaping: doe})
	case xpathComment:
		r.appendResult(target, &xsltResultNode{Kind: xsltResultComment, Value: n.value})
	case xpathPI:
		r.appendResult(target, &xsltResultNode{Kind: xsltResultPI, Name: xsltName{Local: n.name}, Value: n.value})
	}
	return nil
}
func (r *xsltRuntime) copyNodes(target xsltTarget, nodes []*xpathNode) {
	type item struct {
		target xsltTarget
		node   *xpathNode
	}
	var stack []item
	for j := len(nodes) - 1; j >= 0; j-- {
		stack = checkedAppend(&r.frame.p, stack, item{target, nodes[j]})
	}
	for len(stack) > 0 && r.frame.poll() {
		last := len(stack) - 1
		v := stack[last]
		stack = stack[:last]
		n := v.node
		out := r.shallowCopy(v.target, n)
		next := v.target
		if n.kind == xpathElement {
			next = xsltTarget{parent: out}
			for _, a := range n.attributes {
				r.shallowCopy(next, a)
			}
		} else if n.kind != xpathRoot {
			continue
		}
		for j := len(n.children) - 1; j >= 0; j-- {
			stack = checkedAppend(&r.frame.p, stack, item{next, n.children[j]})
		}
	}
}
func (r *xsltRuntime) copyFragment(target xsltTarget, tree *xsltResultTree) {
	type item struct {
		target xsltTarget
		node   *xsltResultNode
	}
	var stack []item
	for j := len(tree.Children) - 1; j >= 0; j-- {
		stack = checkedAppend(&r.frame.p, stack, item{target, tree.Children[j]})
	}
	for len(stack) > 0 && r.frame.poll() {
		last := len(stack) - 1
		v := stack[last]
		stack = stack[:last]
		copy := *v.node
		copy.Children = nil
		copy.Attributes = append([]xsltResultAttribute(nil), v.node.Attributes...)
		copy.Namespaces = append([]xsltNamespace(nil), v.node.Namespaces...)
		r.appendResult(v.target, &copy)
		for j := len(v.node.Children) - 1; j >= 0; j-- {
			stack = checkedAppend(&r.frame.p, stack, item{xsltTarget{parent: &copy}, v.node.Children[j]})
		}
	}
}
