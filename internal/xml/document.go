package xml

import (
	"context"
	"strings"
	"unicode/utf8"
)

// This tree is private to one collecting operation, never retained by Result.
// Expanded occurrences own their nodes; preserved references may share immutable
// replacement contributions, which XPath reads without projecting them as axes.
type nodeKind uint8

const (
	elementNode nodeKind = iota
	textNode
	cdataNode
	commentNode
	piNode
	doctypeNode
	entityReferenceNode
)

type document struct{ nodes []*node }
type node struct {
	kind                            nodeKind
	name, prefix, local, uri, value string
	attributes, namespaces          []nodeAttribute
	children                        []*node
	doctype                         *doctype
	textParts                       []string
	dataPresent                     bool
}
type nodeAttribute struct {
	name, prefix, local, uri, value string
	defaulted, id                   bool
}
type doctype struct {
	name, publicID, systemID, subset, raw string
	hasSubset, external, public           bool
	declarations                          []dtdDeclaration
}
type contentModel struct {
	kind       byte
	name       string
	occurrence byte
	children   []*contentModel
}
type dtdDeclaration struct {
	kind, name, raw, value, originalValue, publicID, systemID, notation string
	parameter, external, unparsed, dataPresent, public, systemPresent   bool
	attributes                                                          []attributeDefinition
	model                                                               *contentModel
	elementType                                                         string
}

func parseExpanded(ctx context.Context, input string, opts Options) (Result, *document, error) {
	return parse(ctx, input, opts, true)
}
func (p *parser) appendNode(n *node) {
	if p.tree == nil || p.err != nil {
		return
	}
	p.err = p.ctx.Err()
	if p.err != nil {
		return
	}
	if n.kind == textNode {
		p.appendText(n.value)
		return
	}
	if p.parent != nil {
		p.parent.children = checkedAppend(p, p.parent.children, n)
	} else {
		p.tree.nodes = checkedAppend(p, p.tree.nodes, n)
	}
}

// Literal source needs line normalization; decoded reference scalars do not.
func (p *parser) collectValue(kind nodeKind, s string) {
	if s != "" || kind != textNode {
		p.noteKind(kind)
	}
	if p.tree == nil || s == "" && kind == textNode {
		return
	}
	if kind == textNode {
		p.collectCharacters(s, false, p.i)
		return
	}
	value := p.normalizeLines(s)
	if p.err == nil {
		p.appendNode(&node{kind: kind, value: value})
	}
}

// collectCharacters receives one grammar callback, before text coalescing. Its
// lookahead is the raw cursor after libxml's normalized next-character read.
func (p *parser) collectCharacters(s string, complex bool, lookahead int) {
	if s != "" {
		p.noteKind(textNode)
	}
	if p.tree == nil || s == "" || p.err != nil {
		return
	}
	if p.ignoreBlanks && p.parent != nil {
		if p.ignorableBlank(s, lookahead) {
			return
		}
		if (complex || space(s[0])) && p.spaceState == -1 {
			p.spaceState = -2
		}
	}
	value := p.normalizeLines(s)
	if p.err == nil {
		p.appendText(value)
	}
}
func (p *parser) normalizeLines(s string) string {
	if p.err != nil {
		return ""
	}
	// The bounded scan avoids an uninterruptible strings.Replace over long input.
	first := -1
	for i := range len(s) {
		if i&4095 == 0 {
			p.err = p.ctx.Err()
			if p.err != nil {
				return ""
			}
		}
		if s[i] == '\r' {
			first = i
			break
		}
	}
	if first < 0 {
		p.err = p.ctx.Err()
		return s
	}
	var b strings.Builder
	p.namespaceCopy(&b, s[:first])
	for i := first; i < len(s) && p.err == nil; i++ {
		if i&4095 == 0 {
			p.err = p.ctx.Err()
			if p.err != nil {
				break
			}
		}
		if s[i] == '\r' {
			if !p.builderByte(&b, '\n') {
				break
			}
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
		} else {
			if !p.builderByte(&b, s[i]) {
				break
			}
		}
	}
	if p.err == nil {
		p.err = p.ctx.Err()
	}
	return b.String()
}
func (p *parser) collectElement(name, local string, attrs []ordinaryAttribute, ns []nodeAttribute) *node {
	if p.tree == nil {
		return nil
	}
	n := &node{kind: elementNode, name: name, local: local, prefix: attributePrefix(name, local), namespaces: ns}
	n.uri = p.boundURI(n.prefix, true)
	for _, a := range attrs {
		p.err = p.ctx.Err()
		if p.err != nil {
			return nil
		}
		prefix := attributePrefix(a.name, a.local)
		n.attributes = checkedAppend(p, n.attributes, nodeAttribute{name: a.name, local: a.local, prefix: prefix, uri: p.boundURI(prefix, false), value: a.value, defaulted: a.defaulted, id: a.id})
		if p.err != nil {
			return nil
		}
	}
	return n
}
func (p *parser) boundURI(prefix string, element bool) string {
	if prefix == "xml" {
		return xmlNamespaceURI
	}
	if prefix == "" && !element {
		return ""
	}
	id := p.namespaceBindings[prefix]
	if id >= 2 {
		return p.namespaceURIs[id-2].value
	}
	return ""
}
func (p *parser) scalarText(r rune) {
	p.noteKind(textNode)
	if p.tree != nil {
		p.appendText(string(r))
	}
}
func (p *parser) appendValue(b *strings.Builder, s string) bool {
	if !appendSize(b.Len(), len(s)) {
		p.err = appendSizeError(b.Len(), len(s))
		return false
	}
	p.namespaceCopy(b, s)
	return p.err == nil
}
func (p *parser) appendScalar(b *strings.Builder, r rune) bool {
	var data [4]byte
	n := utf8.EncodeRune(data[:], r)
	return p.appendValue(b, string(data[:n]))
}
func (p *parser) collectMarkup(kind nodeKind, name string, start, end, rawStart int) {
	if !p.inDTD {
		p.noteKind(kind)
	}
	if p.tree == nil {
		return
	}
	value := p.normalizeLines(p.s[start:end])
	if p.inDTD {
		label := "COMMENT"
		if kind == piNode {
			label = "PI"
		}
		p.dtdState.doctype.declarations = checkedAppend(p, p.dtdState.doctype.declarations, dtdDeclaration{kind: label, name: name, raw: p.s[rawStart:p.i], value: value, dataPresent: kind == piNode && p.s[rawStart+2+len(name)] != '?'})
	} else {
		p.appendNode(&node{kind: kind, name: name, value: value, dataPresent: kind == piNode && p.s[rawStart+2+len(name)] != '?'})
	}
}
func (p *parser) cloneNode(original *node) *node {
	p.err = p.ctx.Err()
	if p.err != nil {
		return nil
	}
	copy := *original
	copy.attributes = nil
	copy.namespaces = nil
	copy.children = nil
	copy.textParts = nil
	for _, a := range original.attributes {
		copy.attributes = checkedAppend(p, copy.attributes, a)
		if p.err != nil {
			return nil
		}
	}
	for _, a := range original.namespaces {
		copy.namespaces = checkedAppend(p, copy.namespaces, a)
		if p.err != nil {
			return nil
		}
	}
	for _, child := range original.children {
		n := p.cloneNode(child)
		if p.err != nil {
			return nil
		}
		copy.children = checkedAppend(p, copy.children, n)
		if p.err != nil {
			return nil
		}
	}
	return &copy
}

// Only top-level content of a balanced entity determines its release owner bit.
// Adjacent text callbacks coalesce; no validation-only payload is constructed.
func (p *parser) noteKind(kind nodeKind) {
	if p.entityDepth == 0 || p.depth != 0 {
		return
	}
	if kind != textNode {
		p.entityShape = 2
	} else if p.entityShape == 0 {
		p.entityShape = 1
	}
}
func (p *parser) noteEntity(e *entity) {
	if e.shape == 1 {
		p.noteKind(textNode)
	} else if e.shape == 2 {
		p.noteKind(elementNode)
	}
}
func (p *parser) appendText(value string) {
	if p.tree == nil || p.err != nil || value == "" {
		return
	}
	p.err = p.ctx.Err()
	if p.err != nil {
		return
	}
	nodes := &p.tree.nodes
	if p.parent != nil {
		nodes = &p.parent.children
	}
	if len(*nodes) != 0 {
		last := (*nodes)[len(*nodes)-1]
		if last.kind == textNode {
			if last.textParts == nil {
				last.textParts = checkedAppend(p, []string(nil), last.value)
			}
			if p.err == nil {
				last.textParts = checkedAppend(p, last.textParts, value)
			}
			return
		}
	}
	*nodes = checkedAppend(p, *nodes, &node{kind: textNode, value: value})
}

// Flatten once, not on each tiny reference. Published trees retain only owned
// immutable values, never the collector's piece slices or parser scratch.
func (p *parser) finishDocument() {
	if p.tree == nil {
		return
	}
	for _, n := range p.tree.nodes {
		p.finishNode(n)
		if p.err != nil {
			return
		}
	}
}
func (p *parser) finishNode(n *node) {
	p.err = p.ctx.Err()
	if p.err != nil {
		return
	}
	if n.textParts != nil {
		n.value = p.joinMessage(n.textParts...)
		if p.err != nil {
			return
		}
		n.textParts = nil
	}
	for _, child := range n.children {
		p.finishNode(child)
		if p.err != nil {
			return
		}
	}
}
