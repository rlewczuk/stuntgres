package xml

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type SerializeOptions struct {
	Mode   Mode
	Indent bool
}
type NotDocumentError struct{}

func (*NotDocumentError) Error() string { return "not an XML document" }

// Serialize converts an immutable XML value to characters. Typed XML transport
// declaration normalization is deliberately owned by OutputText/OutputBinary.
func Serialize(ctx context.Context, value Result, opts SerializeOptions) (string, []Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if !value.valid || opts.Mode > Document {
		return "", nil, fmt.Errorf("invalid XML serialization options or value")
	}
	if !opts.Indent && opts.Mode == Content {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		return value.original, nil, nil
	}
	parsed, tree, events, err := parseConfigured(ctx, value.original, Options{Mode: opts.Mode}, parsePolicy{collect: opts.Indent, ignoreBlanks: opts.Indent})
	if err != nil {
		var pe *ParseError
		if errors.As(err, &pe) {
			return "", events, &NotDocumentError{}
		}
		return "", events, err
	}
	if !opts.Indent {
		if err := ctx.Err(); err != nil {
			return "", events, err
		}
		return value.original, events, nil
	}
	w := serializer{p: parser{ctx: ctx}}
	decl, detail, err := readContentDeclaration(ctx, value.original)
	if err != nil {
		return "", events, err
	}
	if parsed.effective == Document && detail == "" && decl.Present && decl.End > 0 {
		w.utf8 = true
		w.put("<?xml version=\"")
		w.put(parsed.declaration.Version)
		w.put("\" encoding=\"UTF-8\"")
		switch parsed.declaration.Standalone {
		case Yes:
			w.put(" standalone=\"yes\"")
		case No:
			w.put(" standalone=\"no\"")
		}
		w.put("?>\n")
	}
	if parsed.effective == Document {
		for _, n := range tree.nodes {
			if !w.poll() {
				break
			}
			if n.kind == doctypeNode && isXHTML(n.doctype) {
				w.xhtml = true
			}
		}
	}
	for i, n := range tree.nodes {
		if !w.poll() {
			break
		}
		if parsed.effective == Content && i > 0 && n.kind != textNode {
			w.put("\n")
		}
		w.node(n, 0, true, nil)
		if parsed.effective == Document {
			w.put("\n")
		}
	}
	if w.p.err != nil {
		return "", events, w.p.err
	}
	out := w.b.String()
	if opts.Mode == Document {
		end := len(out)
		for end > 0 && (out[end-1] == '\r' || out[end-1] == '\n') {
			if end&4095 == 0 && !w.poll() {
				return "", events, w.p.err
			}
			end--
		}
		out = out[:end]
	}
	if err := ctx.Err(); err != nil {
		return "", events, err
	}
	return out, events, nil
}

type serializer struct {
	p           parser
	b           strings.Builder
	utf8, xhtml bool
}

func (w *serializer) poll() bool {
	if w.p.err == nil {
		w.p.err = w.p.ctx.Err()
	}
	return w.p.err == nil
}
func (w *serializer) put(s string) {
	if w.poll() {
		w.p.appendValue(&w.b, s)
	}
}
func (w *serializer) indent(depth int) {
	w.put("                                                            "[:min(60, max(0, depth)*2)])
}
func (w *serializer) escape(s string, attribute bool) {
	start := 0
	nextPoll := 0
	for i := 0; i < len(s) && w.p.err == nil; {
		if i >= nextPoll {
			if !w.poll() {
				return
			}
			nextPoll = i + 4096
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		replacement := ""
		switch r {
		case '&':
			replacement = "&amp;"
		case '<':
			replacement = "&lt;"
		case '>':
			replacement = "&gt;"
		case '"':
			if attribute {
				replacement = "&quot;"
			}
		case '\t':
			if attribute {
				replacement = "&#9;"
			}
		case '\n':
			if attribute {
				replacement = "&#10;"
			}
		case '\r':
			if attribute || w.utf8 {
				replacement = "&#13;"
			} else {
				replacement = "&#xD;"
			}
		}
		if !attribute && !w.utf8 && r >= 128 {
			var buf [16]byte
			v := strconv.AppendInt(buf[:0], int64(r), 16)
			for j := range v {
				if v[j] >= 'a' && v[j] <= 'f' {
					v[j] -= 32
				}
			}
			w.put(s[start:i])
			w.put("&#x")
			w.put(string(v))
			w.put(";")
			i += n
			start = i
			continue
		}
		if replacement != "" {
			w.put(s[start:i])
			w.put(replacement)
			start = i + n
		}
		i += n
	}
	w.put(s[start:])
}
func (w *serializer) attribute(name, value string) {
	w.put(" ")
	w.put(name)
	w.put("=\"")
	w.escape(value, true)
	w.put("\"")
}
func (w *serializer) node(n *node, depth int, format bool, parent *node) {
	if !w.poll() {
		return
	}
	switch n.kind {
	case textNode:
		w.escape(n.value, false)
	case commentNode:
		w.put("<!--")
		w.put(n.value)
		w.put("-->")
	case piNode:
		w.put("<?")
		w.put(n.name)
		if n.dataPresent {
			w.put(" ")
			w.put(n.value)
		}
		w.put("?>")
	case entityReferenceNode:
		w.put("&")
		w.put(n.name)
		w.put(";")
	case cdataNode:
		w.put("<![CDATA[")
		start := 0
		for i := 0; i+2 < len(n.value); i++ {
			if i&4095 == 0 && !w.poll() {
				return
			}
			if n.value[i:i+3] == "]]>" {
				w.put(n.value[start : i+2])
				w.put("]]><![CDATA[")
				start = i + 2
				i++
			}
		}
		w.put(n.value[start:])
		w.put("]]>")
	case doctypeNode:
		w.saveDTD(n.doctype)
	case elementNode:
		w.element(n, depth, format, parent)
	}
}
func (w *serializer) element(n *node, depth int, format bool, parent *node) {
	w.put("<")
	w.put(n.name)
	for _, a := range n.namespaces {
		if !w.poll() {
			return
		}
		w.attribute(a.name, a.value)
	}
	if w.xhtml && n.local == "html" && n.prefix == "" && n.uri == "" && len(n.namespaces) == 0 {
		w.attribute("xmlns", xhtmlURI)
	}
	if w.xhtml {
		w.xhtmlAttributes(n)
	} else {
		for _, a := range n.attributes {
			if !w.poll() {
				return
			}
			w.attribute(a.name, a.value)
		}
	}
	addmeta := w.xhtml && n.local == "head" && parent != nil && parent.local == "html" && depth == 1 && !w.hasContentType(n)
	if len(n.children) == 0 {
		if !w.xhtml {
			w.put("/>")
			return
		}
		if !addmeta && xhtmlEmpty(n) {
			w.put(" />")
			return
		}
		w.put(">")
		if addmeta {
			w.meta(depth, format)
			if format {
				w.put("\n")
			}
		}
		w.close(n)
		return
	}
	w.put(">")
	if addmeta {
		w.meta(depth, format)
	}
	childFormat := format
	for _, child := range n.children {
		if !w.poll() {
			return
		}
		if child.kind == textNode || child.kind == entityReferenceNode || !w.xhtml && child.kind == cdataNode {
			childFormat = false
			break
		}
	}
	if childFormat {
		w.put("\n")
	}
	for _, child := range n.children {
		if !w.poll() {
			return
		}
		if childFormat && (child.kind == elementNode || !w.xhtml && (child.kind == commentNode || child.kind == piNode)) {
			w.indent(depth + 1)
		}
		w.node(child, depth+1, childFormat, n)
		if childFormat {
			w.put("\n")
		}
	}
	if childFormat {
		w.indent(depth)
	}
	w.close(n)
}
func (w *serializer) close(n *node) { w.put("</"); w.put(n.name); w.put(">") }
