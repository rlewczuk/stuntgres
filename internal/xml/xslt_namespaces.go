package xml

import "strings"

// The non-refactored 1.1.35 compiler relocates locally excluded prefixed
// declarations to the stylesheet root before collecting template namespaces.
// Only root exclusions remain active when that inherited list is constructed;
// a local exclusion is not a result-time URI filter.
func (c *xsltCompiler) preprocessNamespaces(root *node) {
	type visit struct {
		node     *node
		bindings map[string]string
		excluded map[string]bool
	}
	original := len(root.namespaces)
	stack := []visit{{node: root}}
	for len(stack) > 0 && c.p.err == nil {
		c.p.err = c.p.ctx.Err()
		if c.p.err != nil {
			break
		}
		last := len(stack) - 1
		v := stack[last]
		stack = stack[:last]
		n := v.node
		if n.kind != elementNode {
			continue
		}
		bindings := xsltCloneBindings(v.bindings)
		for _, ns := range n.namespaces {
			prefix := ns.local
			if ns.name == "xmlns" {
				prefix = ""
			}
			bindings[prefix] = ns.value
		}
		excluded := v.excluded
		for _, a := range n.attributes {
			if a.local != "exclude-result-prefixes" || !(n == root && n.uri == xsltNamespaceURI && a.uri == "" || n.uri != xsltNamespaceURI && a.uri == xsltNamespaceURI) {
				continue
			}
			excluded = make(map[string]bool, len(v.excluded))
			for uri := range v.excluded {
				excluded[uri] = true
			}
			for _, prefix := range strings.Fields(a.value) {
				if prefix == "#default" {
					prefix = ""
				}
				if uri, ok := bindings[prefix]; ok {
					excluded[uri] = true
				}
			}
		}
		if n != root && len(excluded) > 0 {
			out := n.namespaces[:0]
			for _, ns := range n.namespaces {
				if ns.name != "xmlns" && excluded[ns.value] {
					root.namespaces = checkedAppend(&c.p, root.namespaces, ns)
				} else {
					out = checkedAppend(&c.p, out, ns)
				}
			}
			n.namespaces = out
		}
		if n.uri == xsltNamespaceURI && n.local == "text" {
			continue
		}
		for j := len(n.children) - 1; j >= 0; j-- {
			// Foreign top-level data is not a sequence constructor.
			if n == root && n.uri == xsltNamespaceURI && n.children[j].kind == elementNode && n.children[j].uri != xsltNamespaceURI {
				continue
			}
			stack = checkedAppend(&c.p, stack, visit{n.children[j], bindings, excluded})
		}
	}
	if c.p.err != nil || len(root.namespaces) == original {
		return
	}
	// Relocation prepends each declaration in the release's linked list. Batch
	// the equivalent slice rotation instead of copying the root for each move.
	for low, high := 0, len(root.namespaces)-1; low < high; low, high = low+1, high-1 {
		if c.p.err = c.p.ctx.Err(); c.p.err != nil {
			return
		}
		root.namespaces[low], root.namespaces[high] = root.namespaces[high], root.namespaces[low]
	}
	for low, high := len(root.namespaces)-original, len(root.namespaces)-1; low < high; low, high = low+1, high-1 {
		if c.p.err = c.p.ctx.Err(); c.p.err != nil {
			return
		}
		root.namespaces[low], root.namespaces[high] = root.namespaces[high], root.namespaces[low]
	}
}
