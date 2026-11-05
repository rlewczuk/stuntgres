package xml

import (
	"context"
	"fmt"
)

// LegacySetKind distinguishes raw node dumps from XPath string-value lists,
// independently of whether the caller supplied an empty separator.
type LegacySetKind uint8

const (
	LegacyNodeset LegacySetKind = iota
	LegacyList
)

type LegacySetResult struct {
	Null        bool
	Text        string
	Unsupported int
}

// XPathSet consumes its invocation-private frame immediately. Only final text
// and the actual unsupported XPath result classification cross this boundary.
func XPathSet(ctx context.Context, input, path, root, item, separator string, kind LegacySetKind) (LegacySetResult, error) {
	if kind > LegacyList {
		return LegacySetResult{}, fmt.Errorf("invalid legacy XPath set kind")
	}
	var declaration Declaration
	var header *Declaration
	if kind == LegacyNodeset {
		header = &declaration
	}
	f, value, success, err := legacyXPath(ctx, input, path, false, header)
	if err != nil {
		return LegacySetResult{}, err
	}
	if !success {
		return LegacySetResult{Null: true}, nil
	}
	var result LegacySetResult
	switch value.kind {
	case xpathString:
		result.Text = value.text
	case xpathBoolean, xpathNumber:
		result.Text = "<unsupported/>"
		result.Unsupported = 2
		if value.kind == xpathNumber {
			result.Unsupported = 3
		}
	case xpathNodes:
		w := serializer{p: parser{ctx: ctx}, utf8: true}
		if kind == LegacyNodeset {
			legacyTag(&w, root, false)
		}
		for i, n := range value.nodes {
			if !w.poll() {
				break
			}
			if kind == LegacyList {
				if i != 0 {
					w.put(separator)
				}
				f.appendStringValue(&w.b, n)
				if f.p.err != nil {
					return LegacySetResult{}, f.p.err
				}
			} else {
				legacyTag(&w, item, false)
				legacyDumpNode(&w, f, n, header)
				legacyTag(&w, item, true)
			}
		}
		if kind == LegacyNodeset {
			legacyTag(&w, root, true)
		}
		if w.p.err != nil {
			return LegacySetResult{}, w.p.err
		}
		result.Text = w.b.String()
	default:
		return LegacySetResult{}, fmt.Errorf("invalid legacy XPath result")
	}
	if f.p.err != nil {
		return LegacySetResult{}, f.p.err
	}
	if err := ctx.Err(); err != nil {
		return LegacySetResult{}, err
	}
	return result, nil
}

func legacyTag(w *serializer, name string, close bool) {
	if name == "" {
		return
	}
	w.put("<")
	if close {
		w.put("/")
	}
	w.put(name)
	w.put(">")
}

func legacyDumpNode(w *serializer, f *xpathFrame, n *xpathNode, declaration *Declaration) {
	if !w.poll() {
		return
	}
	switch n.kind {
	case xpathRoot:
		version := declaration.Version
		if version == "" {
			version = "1.0"
		}
		w.put("<?xml version=\"")
		w.put(version)
		w.put("\" encoding=\"UTF-8\"")
		switch declaration.Standalone {
		case Yes:
			w.put(" standalone=\"yes\"")
		case No:
			w.put(" standalone=\"no\"")
		}
		w.put("?>\n")
		for _, source := range f.tree.document.nodes {
			if !w.poll() {
				return
			}
			w.node(source, 0, false, nil)
			w.put("\n")
		}
	case xpathElement:
		// Raw xmlNodeDump retains physical nsDef lists; unlike subtree copying,
		// it must not reconcile namespace bindings inherited from ancestors.
		w.node(n.source, 0, false, nil)
	case xpathAttribute:
		w.attribute(n.name, n.value)
	case xpathNamespace:
		w.put(n.value)
	case xpathText:
		if n.source.kind == cdataNode {
			w.node(&node{kind: cdataNode, value: n.value}, 0, false, nil)
		} else {
			w.escape(n.value, false)
		}
	case xpathComment:
		w.node(&node{kind: commentNode, value: n.value}, 0, false, nil)
	case xpathPI:
		w.node(&node{kind: piNode, name: n.name, value: n.value, dataPresent: n.value != ""}, 0, false, nil)
	}
}
