package xml

import (
	"strings"
	"unicode/utf8"
)

// Admitted namespace declarations have their own inline duplicate collector.
// The first name shares immutable source storage; only a second needs a map.
type attributeNames struct {
	first string
	rest  map[string]struct{}
}

func (n *attributeNames) add(name string) bool {
	if n.first == "" {
		n.first = name
		return false
	}
	if n.rest == nil {
		n.rest = map[string]struct{}{n.first: {}}
	}
	_, duplicate := n.rest[name]
	n.rest[name] = struct{}{}
	return duplicate
}

const xmlNamespaceURI = "http://www.w3.org/XML/1998/namespace"
const xmlnsNamespaceURI = "http://www.w3.org/2000/xmlns/"

// Admission facts remain separate from the operation-only normalized URI.
// Unchanged, contiguous URI spelling shares source storage. A transformed or
// discontinuous contribution starts a buffer only when necessary; hashing streams
// through the value scan. end tracks retained bytes, not consumed references.
// Canonical scalar bytes need no deferred ampersand escape for equality. CONTENT
// attribute construction separately counts five bytes per ampersand reference;
// DOCUMENT counts one. attribute admits canonical contributions only after its
// mode-specific length check, bounding retained/copy storage by the same limit.
// xml:space reuses this concrete capture with construction set: its warning
// needs actual mode-specific value bytes, not namespace comparison bytes.
type namespaceValue struct {
	matches                                      uint8
	length                                       int
	start, end                                   int
	hash                                         uint64
	uri                                          strings.Builder
	transformed, capture, construction, deferAmp bool
	owned                                        bool
	value                                        string
}

func (v *namespaceValue) append(scalar rune) {
	if v.matches == 0 {
		v.length = 1
		return
	}
	if v.length >= len(xmlNamespaceURI) || scalar != rune(xmlNamespaceURI[v.length]) {
		v.matches &^= 1
	}
	if v.length >= len(xmlnsNamespaceURI) || scalar != rune(xmlnsNamespaceURI[v.length]) {
		v.matches &^= 2
	}
	v.length++
}
func (v *namespaceValue) admitted(name, local string) bool {
	// xmlParseStartTag2 excludes these declarations before duplicate lookup.
	// Invalid/relative OTHER URIs are still collected. Default undeclarations
	// (empty xmlns) are collected; empty prefixed declarations are not.
	if v.matches&1 != 0 && v.length == len(xmlNamespaceURI) || v.matches&2 != 0 && v.length == len(xmlnsNamespaceURI) {
		return false
	}
	if name == "xmlns" {
		return true
	}
	return local != "xml" && local != "xmlns" && v.length != 0
}

func (p *parser) namespaceAppend(v *namespaceValue, scalar rune, start, end int) {
	if !v.construction {
		v.append(scalar)
	}
	if !v.capture {
		return
	}
	var encoded [5]byte
	n := utf8.EncodeRune(encoded[:], scalar)
	if v.construction {
		if scalar == '&' && v.deferAmp {
			n = copy(encoded[:], "&#38;")
		}
		// COPY_BUF retains literal single bytes after a decoder failure; references
		// still contribute their decoded scalar, even in that failed parser state.
		if p.failedSingleByte && end-start == 1 && p.s[start] >= 128 {
			encoded[0] = byte(scalar)
			n = 1
		}
	} else {
		for _, b := range encoded[:n] {
			v.hash = (v.hash ^ uint64(b)) * 1099511628211
		}
	}
	if !v.transformed && start == v.end && p.s[start:end] == string(encoded[:n]) {
		v.end = end
		return
	}
	if !v.transformed {
		v.transformed = true
		p.namespaceCopy(&v.uri, p.s[v.start:v.end])
	}
	if p.ensureBuilder(&v.uri, n) {
		v.uri.Write(encoded[:n])
	}
	v.end = end
}

func (p *parser) namespaceCopy(dst *strings.Builder, s string) {
	if !p.ensureBuilder(dst, len(s)) {
		return
	}
	for len(s) > 0 {
		p.err = p.ctx.Err()
		if p.err != nil {
			return
		}
		n := min(4096, len(s))
		dst.WriteString(s[:n])
		s = s[n:]
	}
	if p.err == nil {
		p.err = p.ctx.Err()
	}
}

type namespaceURI struct {
	value string
	next  int
}
type namespaceUndo struct {
	prefix   string
	previous int
}

func (p *parser) namespaceRestore(undo []namespaceUndo) {
	for i := len(undo) - 1; i >= 0; i-- {
		if p.err == nil {
			p.err = p.ctx.Err()
		}
		binding := undo[i]
		if binding.previous == 0 {
			delete(p.namespaceBindings, binding.prefix)
		} else {
			p.namespaceBindings[binding.prefix] = binding.previous
		}
	}
}

type ordinaryAttribute struct {
	name, local, value string
	defaulted, id      bool
}
type attributeIdentity struct{ prefix, local string }
type expandedAttribute struct {
	local string
	uri   int
}

// URI zero is unbound, one is the implicit xml binding. All remaining IDs
// belong only to this parser operation, never the immutable Result.
func (p *parser) namespaceIntern(v *namespaceValue) int {
	value := v.value
	if !v.owned {
		value = p.s[v.start:v.end]
		if v.transformed {
			value = v.uri.String()
		}
	}
	if p.namespaceHashes == nil {
		p.namespaceHashes = make(map[uint64]int)
	}
	for id := p.namespaceHashes[v.hash]; id != 0; id = p.namespaceURIs[id-2].next {
		p.err = p.ctx.Err()
		if p.err != nil {
			return 0
		}
		if p.namespaceEqual(value, p.namespaceURIs[id-2].value) {
			return id
		}
		if p.err != nil {
			return 0
		}
	}
	id := len(p.namespaceURIs) + 2
	p.namespaceURIs = checkedAppend(p, p.namespaceURIs, namespaceURI{value, p.namespaceHashes[v.hash]})
	if p.err != nil {
		return 0
	}
	p.namespaceHashes[v.hash] = id
	return id
}

func (p *parser) namespaceEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for len(a) > 0 {
		p.err = p.ctx.Err()
		if p.err != nil {
			return false
		}
		n := min(4096, len(a))
		if a[:n] != b[:n] {
			return false
		}
		a, b = a[n:], b[n:]
	}
	return true
}

func attributePrefix(name, local string) string {
	if len(name) == len(local) {
		return ""
	}
	return name[:len(name)-len(local)-1]
}

func (p *parser) attributeDuplicates(attributes []ordinaryAttribute) {
	if len(attributes) < 2 {
		return
	}
	lexical := make(map[attributeIdentity]int)
	expanded := make(map[expandedAttribute]int)
	for index, attr := range attributes {
		p.err = p.ctx.Err()
		if p.err != nil {
			return
		}
		prefix := attributePrefix(attr.name, attr.local)
		uri := 0
		if prefix == "xml" {
			uri = 1
		} else if prefix != "" {
			uri = p.namespaceBindings[prefix]
		}
		key := attributeIdentity{prefix, attr.local}
		firstLexical, lexicalMatch := lexical[key]
		expandedKey := expandedAttribute{attr.local, uri}
		firstExpanded, expandedMatch := expanded[expandedKey]
		// The release breaks on its first preceding match, including a filtered
		// namespace-domain collision. A lexical match wins only if it comes first.
		if lexicalMatch && (!expandedMatch || uri == 0 || firstLexical <= firstExpanded) {
			p.event("XML_ERR_ATTRIBUTE_REDEFINED", p.joinMessage("Attribute ", attr.name, " redefined"))
			if p.err != nil || p.stopped {
				return
			}
		}
		if p.policy.strictNamespaces && expandedMatch && uri != 0 && !lexicalMatch {
			p.namespaceError("XML_NS_ERR_ATTRIBUTE_REDEFINED", p.joinMessage("Namespaced Attribute ", attr.local, " in '", p.boundURI(prefix, false), "' redefined"))
		}
		if !lexicalMatch {
			lexical[key] = index
		}
		if uri != 0 && !expandedMatch {
			expanded[expandedKey] = index
		}
	}
}

func (p *parser) namespaceError(code Condition, message string) {
	p.namespaceEvent(code, message, Error)
}
func (p *parser) namespaceEvent(code Condition, message string, severity Severity) {
	if !p.policy.strictNamespaces {
		return
	}
	before := len(p.events)
	p.eventLevel(code, message, severity)
	if len(p.events) > before {
		p.events[len(p.events)-1].Domain = NamespaceDomain
	}
}
func (p *parser) namespaceDeclaration(name, local string, v *namespaceValue) {
	if !p.policy.strictNamespaces {
		return
	}
	value := p.s[v.start:v.end]
	if v.transformed {
		value = v.uri.String()
	}
	switch {
	case name != "xmlns" && local == "xml":
		if value != xmlNamespaceURI {
			p.namespaceError("XML_NS_ERR_XML_NAMESPACE", "xml namespace prefix mapped to wrong URI")
		}
	case value == xmlNamespaceURI && name == "xmlns":
		p.namespaceError("XML_NS_ERR_XML_NAMESPACE", "xml namespace URI cannot be the default namespace")
	case value == xmlNamespaceURI:
		p.namespaceError("XML_NS_ERR_XML_NAMESPACE", "xml namespace URI mapped to wrong prefix")
	case name != "xmlns" && local == "xmlns":
		p.namespaceError("XML_NS_ERR_XML_NAMESPACE", "redefinition of the xmlns prefix is forbidden")
	case value == xmlnsNamespaceURI:
		p.namespaceError("XML_NS_ERR_XML_NAMESPACE", "reuse of the xmlns namespace name is forbidden")
	case value == "" && name != "xmlns":
		p.namespaceError("XML_NS_ERR_XML_NAMESPACE", p.joinMessage("xmlns:", local, ": Empty XML namespace is not allowed"))
	default:
		if value == "" {
			return
		}
		valid, _ := p.entityURI(value)
		if !valid {
			label := "xmlns"
			if name != "xmlns" {
				label += ":" + local
			}
			p.namespaceError("XML_WAR_NS_URI", p.joinMessage(label, ": '", value, "' is not a valid URI"))
			return
		}
		// Only default relative declarations warn without the pedantic option.
		if name == "xmlns" {
			absolute := false
			for i := range len(value) {
				if i&4095 == 0 {
					p.err = p.ctx.Err()
					if p.err != nil {
						return
					}
				}
				if value[i] == ':' {
					absolute = true
					break
				}
				if value[i] == '/' || value[i] == '?' || value[i] == '#' {
					break
				}
			}
			if !absolute {
				p.namespaceEvent("XML_WAR_NS_URI_RELATIVE", p.joinMessage("xmlns: URI ", value, " is not absolute"), Warning)
			}
		}
	}
}
func (p *parser) namespaceNames(name, local string, attrs []ordinaryAttribute) {
	if !p.policy.strictNamespaces {
		return
	}
	prefix := attributePrefix(name, local)
	if prefix != "" && prefix != "xml" && p.namespaceBindings[prefix] == 0 {
		p.namespaceError("XML_NS_ERR_UNDEFINED_NAMESPACE", p.joinMessage("Namespace prefix ", prefix, " on ", local, " is not defined"))
	}
	for _, a := range attrs {
		p.err = p.ctx.Err()
		if p.err != nil {
			return
		}
		prefix = attributePrefix(a.name, a.local)
		if prefix != "" && prefix != "xml" && p.namespaceBindings[prefix] == 0 {
			p.namespaceError("XML_NS_ERR_UNDEFINED_NAMESPACE", p.joinMessage("Namespace prefix ", prefix, " for ", a.local, " on ", local, " is not defined"))
		}
	}
}
