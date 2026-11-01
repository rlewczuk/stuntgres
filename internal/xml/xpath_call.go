package xml

import (
	"context"
	"fmt"
)

// XPathMapping borrows one invocation's namespace pair. Registration is not XML
// declaration validation; aliases are interpreted only by the XPath compiler.
type XPathMapping struct {
	Alias, URI string
	Null       bool
}
type XPathNamespaceError struct {
	Alias, URI string
	Null       bool
}

func (e *XPathNamespaceError) Error() string {
	if e.Null {
		return "neither namespace name nor URI may be null"
	}
	return fmt.Sprintf("could not register XML namespace with name \"%s\" and URI \"%s\"", e.Alias, e.URI)
}

// XPathResult owns only serialized spellings or a raw numeric scalar. No tree,
// resolver, source Result diagnostics or operation context escapes the call.
type XPathResult struct {
	Items   []string
	Number  float64
	Numeric bool
	Exists  bool
}

// XPath evaluates the same full XPath language as XMLExists and XMLTABLE.
// existsOnly deliberately bypasses all result serialization.
func XPath(ctx context.Context, path string, value Result, mappings []XPathMapping, existsOnly bool) (XPathResult, []Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return XPathResult{}, nil, err
	}
	if path == "" {
		return XPathResult{}, nil, &XPathError{Stage: "empty"}
	}
	f, events, err := documentFrame(ctx, value)
	if err != nil {
		return XPathResult{}, events, err
	}
	if err := f.registerXPathMappings(mappings); err != nil {
		return XPathResult{}, events, err
	}
	expression, err := compileXPath(ctx, path)
	if err != nil {
		return XPathResult{}, events, err
	}
	v := f.eval(expression)
	if f.p.err != nil {
		return XPathResult{}, events, f.p.err
	}
	out := XPathResult{Exists: v.kind != xpathNodes || len(v.nodes) != 0}
	if !existsOnly {
		if v.kind == xpathNumber {
			out.Numeric, out.Number = true, v.number
		} else if v.kind == xpathNodes {
			out.Items = make([]string, len(v.nodes))
			table := Table{frame: f}
			for i, n := range v.nodes {
				w := serializer{p: parser{ctx: ctx}, utf8: true, namespaceAmpNumeric: true}
				switch {
				case n.kind == xpathRoot:
					// Document conversion emits its own UTF8 declaration, not the source's.
					w.put("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
					table.serializeNode(&w, n)
				case n.kind == xpathNamespace:
					// libxml's namespace cast preserves its deferred ampersand spelling.
					xpathEscape(&w, n.value, true)
				case n.kind == xpathAttribute || n.kind == xpathText && n.source.kind != cdataNode:
					xpathEscape(&w, n.value, false)
				default:
					table.serializeNode(&w, n)
				}
				if w.p.err != nil {
					return XPathResult{}, events, w.p.err
				}
				out.Items[i] = w.b.String()
			}
		} else {
			w := serializer{p: parser{ctx: ctx}, utf8: true}
			xpathEscape(&w, f.string(v), false)
			if w.p.err != nil {
				return XPathResult{}, events, w.p.err
			}
			out.Items = []string{w.b.String()}
		}
	}
	if err := ctx.Err(); err != nil {
		return XPathResult{}, events, err
	}
	return out, events, nil
}

func (f *xpathFrame) registerXPathMappings(mappings []XPathMapping) error {
	if len(mappings) != 0 {
		f.bindings = make(map[string]string, len(mappings))
	}
	for _, pair := range mappings {
		if !f.poll() {
			return f.p.err
		}
		if pair.Null || pair.Alias == "" {
			return &XPathNamespaceError{Alias: pair.Alias, URI: pair.URI, Null: pair.Null}
		}
		f.bindings[pair.Alias] = pair.URI
	}
	return nil
}

func xpathEscape(w *serializer, text string, namespace bool) {
	start := 0
	for i := range len(text) {
		if i%4096 == 0 && !w.poll() {
			return
		}
		replacement := ""
		switch text[i] {
		case '&':
			replacement = "&amp;"
			if namespace {
				replacement = "&amp;#38;"
			}
		case '<':
			replacement = "&lt;"
		case '>':
			replacement = "&gt;"
		case '\r':
			replacement = "&#x0d;"
		}
		if replacement != "" {
			w.put(text[start:i])
			w.put(replacement)
			start = i + 1
		}
	}
	w.put(text[start:])
}
