package xml

import (
	"context"
	"strings"
)

type xpathNodeKind uint8

const (
	xpathRoot xpathNodeKind = iota
	xpathElement
	xpathAttribute
	xpathNamespace
	xpathText
	xpathComment
	xpathPI
)

type xpathNode struct {
	kind                             xpathNodeKind
	order                            int
	parent                           *xpathNode
	children, attributes, namespaces []*xpathNode
	source                           *node
	name, local, uri, value          string
}
type xpathTree struct {
	root    *xpathNode
	ordered []*xpathNode
	ids     map[string]*xpathNode
	document *document
}
type xpathFrame struct {
	p                     parser
	tree                  *xpathTree
	node                  *xpathNode
	position, size, depth int
	bindings              map[string]string
}

func (f *xpathFrame) poll() bool {
	if f.p.err == nil {
		f.p.err = f.p.ctx.Err()
	}
	return f.p.err == nil
}
func (f *xpathFrame) add(n *xpathNode) *xpathNode {
	n.order = len(f.tree.ordered)
	f.tree.ordered = checkedAppend(&f.p, f.tree.ordered, n)
	return n
}
func newXPathTree(ctx context.Context, d *document) (*xpathFrame, error) {
	f := &xpathFrame{p: parser{ctx: ctx}, tree: &xpathTree{ids: make(map[string]*xpathNode), document: d}, position: 1, size: 1}
	f.tree.root = f.add(&xpathNode{kind: xpathRoot})
	f.node = f.tree.root
	f.project(d.nodes, f.node)
	return f, f.p.err
}
func (f *xpathFrame) project(nodes []*node, parent *xpathNode) {
	for i := 0; i < len(nodes) && f.poll(); i++ {
		n := nodes[i]
		var x *xpathNode
		switch n.kind {
		case doctypeNode, entityReferenceNode:
			continue
		case elementNode:
			x = f.add(&xpathNode{kind: xpathElement, parent: parent, source: n, name: n.name, local: n.local, uri: n.uri})
			// Namespace identity belongs to the owner, not to the declaration site.
			// libxml lists the implicit xml binding and nearest declarations, including
			// an empty default binding that masks an ancestor's default namespace.
			f.namespace(x, "xml", xmlNamespaceURI)
			seen := map[string]bool{"xml": true}
			for owner := x; owner != nil && owner.kind == xpathElement && f.poll(); owner = owner.parent {
				for j := len(owner.source.namespaces) - 1; j >= 0 && f.poll(); j-- {
					ns := owner.source.namespaces[j]
					prefix := ns.local
					if ns.name == "xmlns" {
						prefix = ""
					}
					if seen[prefix] {
						continue
					}
					seen[prefix] = true
					f.namespace(x, prefix, ns.value)
				}
			}
			for _, a := range n.attributes {
				if !f.poll() {
					return
				}
				if a.defaulted {
					continue
				}
				attribute := f.add(&xpathNode{kind: xpathAttribute, parent: x, name: a.name, local: a.local, uri: a.uri, value: a.value})
				x.attributes = checkedAppend(&f.p, x.attributes, attribute)
				if a.id {
					if _, found := f.tree.ids[a.value]; !found {
						f.tree.ids[a.value] = x
					}
				}
			}
			f.project(n.children, x)
		case textNode, cdataNode:
			// options0 preserves text/CDATA boundaries but coalesces consecutive CDATA
			// callbacks. Both project to XPath text; retain the first source's kind
			// for XML serialization, without changing entity or XPath boundaries.
			value := n.value
			if i+1 < len(nodes) && nodes[i+1].kind == n.kind {
				var b strings.Builder
				f.p.appendValue(&b, value)
				for i+1 < len(nodes) && nodes[i+1].kind == n.kind && f.poll() {
					i++
					f.p.appendValue(&b, nodes[i].value)
				}
				value = b.String()
			}
			if value == "" {
				continue
			}
			x = f.add(&xpathNode{kind: xpathText, parent: parent, source: n, value: value})
		case commentNode:
			x = f.add(&xpathNode{kind: xpathComment, parent: parent, value: n.value})
		case piNode:
			x = f.add(&xpathNode{kind: xpathPI, parent: parent, name: n.name, local: n.name, value: n.value})
		}
		if x != nil {
			parent.children = checkedAppend(&f.p, parent.children, x)
		}
	}
}
func (f *xpathFrame) namespace(owner *xpathNode, prefix, uri string) {
	n := f.add(&xpathNode{kind: xpathNamespace, parent: owner, name: prefix, local: prefix, value: uri})
	owner.namespaces = checkedAppend(&f.p, owner.namespaces, n)
}
func (f *xpathFrame) stringValue(n *xpathNode) string {
	if n.kind != xpathRoot && n.kind != xpathElement {
		return n.value
	}
	if n.kind == xpathRoot {
		for _, child := range n.children {
			if !f.poll() {
				return ""
			}
			if child.kind == xpathElement {
				return f.stringValue(child)
			}
		}
		return ""
	}
	if len(n.source.children) == 1 {
		child := n.source.children[0]
		if child.kind == textNode || child.kind == cdataNode {
			return child.value
		}
	}
	var b strings.Builder
	f.sourceText(&b, n.source)
	return b.String()
}
func (f *xpathFrame) sourceText(b *strings.Builder, n *node) {
	if !f.poll() {
		return
	}
	switch n.kind {
	case textNode, cdataNode:
		f.p.appendValue(b, n.value)
	case elementNode, entityReferenceNode:
		for _, c := range n.children {
			f.sourceText(b, c)
			if f.p.err != nil {
				return
			}
		}
	}
}
func (f *xpathFrame) normalize(nodes []*xpathNode) []*xpathNode {
	if len(nodes) < 2 {
		return nodes
	}
	// In-place heapsort makes each comparison/traversal interruptible without
	// sort callbacks that cannot abort or an operation-sized auxiliary copy.
	sift := func(root, end int) {
		for root < end/2 && f.poll() {
			child := root*2 + 1
			if child+1 < end && nodes[child].order < nodes[child+1].order {
				child++
			}
			if nodes[root].order >= nodes[child].order {
				return
			}
			nodes[root], nodes[child] = nodes[child], nodes[root]
			root = child
		}
	}
	for start := len(nodes)/2 - 1; start >= 0 && f.poll(); start-- {
		sift(start, len(nodes))
	}
	for end := len(nodes) - 1; end > 0 && f.poll(); end-- {
		nodes[0], nodes[end] = nodes[end], nodes[0]
		sift(0, end)
	}
	out := nodes[:0]
	for _, n := range nodes {
		if !f.poll() {
			return nil
		}
		if len(out) == 0 || out[len(out)-1] != n {
			out = append(out, n)
		}
	}
	return out
}
func xpathAncestor(ancestor, n *xpathNode) bool {
	for n = n.parent; n != nil; n = n.parent {
		if n == ancestor {
			return true
		}
	}
	return false
}
func (f *xpathFrame) axis(n *xpathNode, axis string) []*xpathNode {
	var out []*xpathNode
	add := func(n *xpathNode) { out = checkedAppend(&f.p, out, n) }
	var descend func(*xpathNode)
	descend = func(n *xpathNode) {
		for _, c := range n.children {
			if !f.poll() {
				return
			}
			add(c)
			descend(c)
		}
	}
	switch axis {
	case "self":
		add(n)
	case "parent":
		if n.parent != nil {
			add(n.parent)
		}
	case "child":
		return n.children
	case "attribute":
		return n.attributes
	case "namespace":
		return n.namespaces
	case "descendant":
		descend(n)
	case "descendant-or-self":
		add(n)
		descend(n)
	case "ancestor", "ancestor-or-self":
		if axis == "ancestor-or-self" {
			add(n)
		}
		for p := n.parent; p != nil && f.poll(); p = p.parent {
			add(p)
		}
	case "following-sibling", "preceding-sibling":
		if n.parent == nil || n.kind == xpathAttribute || n.kind == xpathNamespace {
			return nil
		}
		siblings := n.parent.children
		index := -1
		for i, c := range siblings {
			if !f.poll() {
				return nil
			}
			if c == n {
				index = i
				break
			}
		}
		if index < 0 {
			return nil
		}
		if axis == "following-sibling" {
			for _, c := range siblings[index+1:] {
				if !f.poll() {
					break
				}
				add(c)
			}
		} else {
			for i := index - 1; i >= 0 && f.poll(); i-- {
				add(siblings[i])
			}
		}
	case "following":
		for _, c := range f.tree.ordered[n.order+1:] {
			if !f.poll() {
				break
			}
			if c.kind != xpathAttribute && c.kind != xpathNamespace && !xpathAncestor(n, c) {
				add(c)
			}
		}
	case "preceding":
		for i := n.order - 1; i >= 0 && f.poll(); i-- {
			c := f.tree.ordered[i]
			if c.kind != xpathAttribute && c.kind != xpathNamespace && !xpathAncestor(c, n) {
				add(c)
			}
		}
	}
	return out
}
func (f *xpathFrame) matches(n *xpathNode, s xpathStep) bool {
	switch s.test {
	case "node":
		return true
	case "text":
		return n.kind == xpathText
	case "comment":
		return n.kind == xpathComment
	case "processing-instruction":
		return n.kind == xpathPI
	case "pi-target":
		return n.kind == xpathPI && n.name == s.name
	}
	principal := xpathElement
	if s.axis == "attribute" {
		principal = xpathAttribute
	} else if s.axis == "namespace" {
		principal = xpathNamespace
	}
	if n.kind != principal {
		return false
	}
	if s.name == "*" {
		return true
	}
	prefix, local, qualified := strings.Cut(s.name, ":")
	if qualified {
		uri, ok := f.namespaceURI(prefix)
		if !ok {
			f.runtimeError("Undefined namespace prefix")
			return false
		}
		return n.uri == uri && (local == "*" || n.local == local)
	}
	return n.local == s.name && n.uri == ""
}

func (f *xpathFrame) namespaceURI(prefix string) (string, bool) {
	if prefix == "xml" { return xmlNamespaceURI, true }
	uri, ok := f.bindings[prefix]
	return uri, ok
}
