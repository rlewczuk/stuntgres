package xml

import (
	"net/url"
	"strconv"
	"strings"
)

var xsltCoreFunctions = map[string]bool{
	"last": true, "position": true, "count": true, "id": true, "local-name": true, "namespace-uri": true, "name": true,
	"string": true, "concat": true, "starts-with": true, "contains": true, "substring-before": true, "substring-after": true, "substring": true, "string-length": true, "normalize-space": true, "translate": true,
	"boolean": true, "not": true, "true": true, "false": true, "lang": true, "number": true, "sum": true, "floor": true, "ceiling": true, "round": true,
	"current": true, "document": true, "key": true, "format-number": true, "unparsed-entity-uri": true, "generate-id": true, "system-property": true, "element-available": true, "function-available": true,
}
var xsltAvailableElements = map[string]bool{
	"apply-imports": true, "apply-templates": true, "attribute": true, "call-template": true, "choose": true, "comment": true, "copy": true, "copy-of": true, "element": true, "fallback": true, "for-each": true, "if": true, "message": true, "number": true, "processing-instruction": true, "text": true, "value-of": true, "variable": true,
	"document": true,
}

func (e *xsltEnvironment) call(f *xpathFrame, name string, args []xpathValue) (xpathValue, bool) {
	r := e.runtime
	min, max := 1, 1
	switch name {
	case "current":
		min, max = 0, 0
	case "document":
		max = 2
	case "key":
		min, max = 2, 2
	case "format-number":
		min, max = 2, 3
	case "generate-id":
		min = 0
	case "unparsed-entity-uri", "system-property", "element-available", "function-available":
	default:
		return xpathValue{}, false
	}
	if len(args) < min || len(args) > max {
		f.runtimeError("Invalid number of arguments")
		return xpathValue{}, true
	}
	first := xpathValue{nodes: []*xpathNode{f.node}}
	if len(args) > 0 {
		first = args[0]
	}
	switch name {
	case "current":
		if e.current == nil {
			return xpathValue{}, true
		}
		return xpathValue{nodes: []*xpathNode{e.current}}, true
	case "document":
		return e.document(f, args), true
	case "key":
		key, ok := xsltQName(f.string(first), f.bindings, false)
		if !ok {
			r.fail()
			return xpathValue{}, true
		}
		info := r.findDocumentInfo(f.node.owner)
		if info == nil {
			r.fail()
			return xpathValue{}, true
		}
		index := r.keyIndex(info, key.key())
		var out []*xpathNode
		add := func(s string) {
			for _, node := range index[s] {
				out = checkedAppend(&f.p, out, node)
			}
		}
		if args[1].kind == xpathNodes {
			for _, n := range args[1].nodes {
				if !f.poll() {
					break
				}
				add(f.stringValue(n))
			}
		} else {
			add(f.string(args[1]))
		}
		return xpathValue{nodes: f.normalize(out)}, true
	case "format-number":
		symbols := r.program.decimals["\x00"]
		if len(args) == 3 {
			n, ok := xsltQName(f.string(args[2]), f.bindings, false)
			if !ok {
				r.fail()
				return xpathStr(""), true
			}
			var found bool
			symbols, found = r.program.decimals[n.key()]
			if !found {
				r.fail()
				return xpathStr(""), true
			}
		}
		text, err := formatXSLTDecimal(f.p.ctx, f.number(first), f.string(args[1]), symbols)
		if err != nil {
			f.p.err = err
		}
		return xpathStr(text), true
	case "generate-id":
		if first.kind != xpathNodes {
			f.runtimeError("Invalid type")
			return xpathValue{}, true
		}
		if len(first.nodes) == 0 {
			return xpathStr(""), true
		}
		n := first.nodes[0]
		return xpathStr("id" + strconv.Itoa(n.owner.ordinal+1) + "n" + strconv.Itoa(n.order)), true
	case "unparsed-entity-uri":
		wanted := f.string(first)
		document := f.node.owner.document
		for _, n := range document.nodes {
			if !f.poll() {
				break
			}
			if n.doctype == nil {
				continue
			}
			for _, d := range n.doctype.declarations {
				if !f.poll() {
					break
				}
				if d.kind == "ENTITY" && d.name == wanted && d.unparsed {
					return xpathStr(xsltResolveBase(document.baseURI, d.systemID)), true
				}
			}
		}
		return xpathStr(""), true
	case "system-property":
		q, ok := xsltQName(f.string(first), f.bindings, false)
		if !ok {
			if strings.Contains(f.string(first), ":") {
				r.fail()
			}
			return xpathStr(""), true
		}
		if q.URI != xsltNamespaceURI {
			return xpathStr(""), true
		}
		switch q.Local {
		case "version":
			return xpathStr("1.0"), true
		case "vendor":
			return xpathStr("stuntgres"), true
		case "vendor-url":
			return xpathStr("https://github.com/rlewczuk/stuntgres"), true
		}
		return xpathStr(""), true
	case "function-available":
		lexical := f.string(first)
		q, ok := xsltQName(lexical, f.bindings, false)
		if !ok && strings.Contains(lexical, ":") {
			r.fail()
		}
		return xpathBool(ok && q.URI == "" && xsltCoreFunctions[q.Local]), true
	case "element-available":
		if e.instruction == nil {
			r.fail()
			return xpathBool(false), true
		}
		lexical := f.string(first)
		q, ok := xsltQName(lexical, f.bindings, true)
		if !ok && strings.Contains(lexical, ":") {
			r.fail()
		}
		return xpathBool(ok && q.URI == xsltNamespaceURI && xsltAvailableElements[q.Local]), true
	}
	return xpathValue{}, true
}
func (e *xsltEnvironment) document(f *xpathFrame, args []xpathValue) xpathValue {
	if len(args) == 2 && args[1].kind != xpathNodes {
		f.runtimeError("Invalid type")
		return xpathValue{}
	}
	r := e.runtime
	base := ""
	if e.instruction != nil {
		base = e.instruction.baseURI
	}
	if len(args) == 2 && len(args[1].nodes) > 0 {
		base = xsltNodeBase(args[1].nodes[0])
	}
	var out []*xpathNode
	load := func(reference, base string) {
		if !f.poll() {
			return
		}
		resolved := xsltResolveBase(base, reference)
		_, err := url.Parse(resolved)
		if err != nil {
			r.fail()
			return
		}
		// Empty URI has a genuine in-memory stylesheet identity, not an empty
		// external-loader result or the source document. Every external access is
		// denied before any filesystem/network operation exists.
		if reference == "" && resolved == "" {
			out = checkedAppend(&f.p, out, r.program.style.root)
			return
		}
		// A nonempty fragment reference still enters xsltLoadDocument after its
		// fragment is separated. Security rejects that resource before the style
		// fallback/XPointer result could make the transformation successful.
		r.fail()
	}
	if args[0].kind == xpathNodes {
		for _, n := range args[0].nodes {
			if !f.poll() {
				break
			}
			nodeBase := base
			if len(args) == 1 {
				nodeBase = xsltNodeBase(n)
			}
			load(f.stringValue(n), nodeBase)
		}
	} else {
		load(f.string(args[0]), base)
	}
	return xpathValue{nodes: f.normalize(out)}
}
func xsltNodeBase(n *xpathNode) string {
	for ; n != nil; n = n.parent {
		if n.source != nil && n.source.baseURI != "" {
			return n.source.baseURI
		}
		if n.kind == xpathRoot {
			return n.owner.document.baseURI
		}
	}
	return ""
}
func (r *xsltRuntime) findDocumentInfo(tree *xpathTree) *xsltDocumentInfo {
	for _, info := range r.registrations {
		if info.tree == tree {
			return info
		}
	}
	// This is the real xsltFindDocument style-doc fallback: XPath's owner stays
	// the stylesheet, while transform document-info aliases the current source
	// registration. Key caches are owned by that registration, never tree hacks.
	if tree == r.program.style {
		return r.registrations[0]
	}
	return nil
}
func (r *xsltRuntime) keyIndex(info *xsltDocumentInfo, key string) map[string][]*xpathNode {
	if index, ok := info.keys[key]; ok {
		return index
	}
	f := r.frame
	token := strconv.Itoa(info.tree.ordinal) + ":" + key
	if r.keysBuilding[token] {
		f.runtimeError("")
		return nil
	}
	r.keysBuilding[token] = true
	defer delete(r.keysBuilding, token)
	index := make(map[string][]*xpathNode)
	oldNode, oldPosition, oldSize, oldBindings, oldEnvironment := f.node, f.position, f.size, f.bindings, f.environment
	defer func() {
		f.node, f.position, f.size, f.bindings, f.environment = oldNode, oldPosition, oldSize, oldBindings, oldEnvironment
	}()
	for _, definition := range r.program.keys[key] {
		f.bindings = definition.instruction.namespaces
		for _, n := range info.tree.ordered {
			if !f.poll() {
				return index
			}
			if n.kind == xpathNamespace {
				continue
			}
			f.node, f.position, f.size = n, 1, 1
			f.environment = &xsltEnvironment{runtime: r, current: n, instruction: definition.instruction}
			matches := false
			for k := range definition.pattern {
				if definition.pattern[k].matches(f, n) {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
			value := f.eval(definition.use)
			add := func(s string) {
				nodes := index[s]
				if len(nodes) == 0 || nodes[len(nodes)-1] != n {
					index[s] = checkedAppend(&f.p, nodes, n)
				}
			}
			if value.kind == xpathNodes {
				for _, v := range value.nodes {
					add(f.stringValue(v))
				}
			} else {
				add(f.string(value))
			}
		}
	}
	for k, nodes := range index {
		index[k] = f.normalize(nodes)
	}
	if f.p.err == nil {
		info.keys[key] = index
	}
	return index
}
func (c *xsltCompiler) decimal(i *xsltInstruction) {
	key := "\x00"
	if name, ok := xsltAttr(i.source, "name"); ok {
		n, valid := xsltQName(name, i.namespaces, false)
		if !valid {
			c.fail("")
			return
		}
		key = n.key()
		// Named formats are installed once. The released parser reports a
		// duplicate generically and returns before reading its symbol fields.
		if _, exists := c.program.decimals[key]; exists {
			return
		}
	}
	d := c.program.decimals["\x00"]
	if key != "\x00" {
		d = xsltDefaultDecimalFormat()
	}
	for _, a := range i.source.attributes {
		if a.uri != "" {
			continue
		}
		if a.local == "Infinity" || a.local == "infinity" {
			d.Infinity = a.value
			continue
		}
		if a.local == "NaN" {
			d.NaN = a.value
			continue
		}
		var target *rune
		switch a.local {
		case "decimal-separator":
			target = &d.DecimalSeparator
		case "grouping-separator":
			target = &d.GroupingSeparator
		case "minus-sign":
			target = &d.MinusSign
		case "percent":
			target = &d.Percent
		case "per-mille":
			target = &d.PerMille
		case "zero-digit":
			target = &d.ZeroDigit
		case "digit":
			target = &d.Digit
		case "pattern-separator":
			target = &d.PatternSeparator
		}
		if target != nil {
			runes := []rune(a.value)
			if len(runes) != 1 {
				c.fail("")
				return
			}
			*target = runes[0]
		}
	}
	seen := make(map[rune]bool)
	for _, symbol := range []rune{d.DecimalSeparator, d.GroupingSeparator, d.Percent, d.PerMille, d.ZeroDigit, d.Digit, d.PatternSeparator} {
		if seen[symbol] {
			c.fail("")
			return
		}
		seen[symbol] = true
	}
	c.program.decimals[key] = d
}
