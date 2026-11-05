package xml

import "strings"

// xsltSplitQName follows the released splitter, which does not validate names.
// In particular, a leading colon remains part of an unprefixed local name.
// User parameters consume this spelling separately from stylesheet validation.
func xsltSplitQName(s string) (prefix, local string) {
	if prefix, local, qualified := strings.Cut(s, ":"); qualified && prefix != "" {
		return prefix, local
	}
	return "", s
}

func xsltLexicalQName(s string) (prefix, local string, valid bool) {
	prefix, local = xsltSplitQName(s)
	return prefix, local, xsltNCName(local) && (prefix == "" || xsltNCName(prefix))
}

func (r *xsltRuntime) dynamicName(i *xsltInstruction, c xsltContext) (xsltName, bool) {
	lexical := r.avt(i, "name", c)
	if i.op == "processing-instruction" {
		_, present := xsltAttr(i.source, "name")
		return xsltName{Local: lexical}, present
	}
	bindings := i.namespaces
	_, explicitNamespace := i.avts["\x00namespace"]
	uri := ""
	if explicitNamespace {
		uri = r.avt(i, "namespace", c)
		if prefix, _ := xsltSplitQName(lexical); prefix != "" {
			bindings = xsltCloneBindings(bindings)
			bindings[prefix] = uri
		}
	}
	name, ok := xsltQName(lexical, bindings, i.op == "element")
	if !ok {
		r.fail()
		prefix, local := xsltSplitQName(lexical)
		uri := bindings[prefix]
		if prefix == "" && i.op != "element" {
			uri = ""
		}
		name = xsltName{Prefix: prefix, Local: local, URI: uri}
	}
	if explicitNamespace {
		name.URI = uri
	}
	if name.URI == "http://www.w3.org/2000/xmlns/" || i.op == "attribute" && name.Local == "xmlns" && name.Prefix == "" {
		r.fail()
		return xsltName{}, false
	}
	if name.URI == xmlNamespaceURI {
		name.Prefix = "xml"
	} else if name.Prefix == "xml" {
		name.Prefix = ""
	}
	if name.Prefix == "xmlns" {
		r.prefixCounter++
		name.Prefix = "ns_1"
	}
	if name.URI == "" {
		name.Prefix = ""
	}
	return name, true
}
