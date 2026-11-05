package xml

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type xsltSaver struct {
	p                           *parser
	out                         xsltOutputEncoder
	props                       xsltOutputProperties
	html, text, indent, finalLF bool
	metaHead, metaNode          *xsltResultNode
	metaContent                 string
	namespaces                  map[string]string
	namespaceUndo               []xsltNamespace
}
type xsltSaveFrame struct {
	node          *xsltResultNode
	children      []*xsltResultNode
	next          int
	format        bool
	htmlBreak     bool
	namespaceBase int
}

// saveXSLT consumes an already namespace-fixed result tree. It deliberately
// neither reparses XML nor mutates a result node (including HTML's META update).
// Ordinary cancellation, allocation-size and invariant failures publish no bytes.
func saveXSLT(ctx context.Context, tree *xsltResultTree, props xsltOutputProperties) (XSLTResult, error) {
	if err := ctx.Err(); err != nil {
		return XSLTResult{}, err
	}
	if tree == nil {
		return XSLTResult{}, fmt.Errorf("nil XSLT result tree")
	}
	if len(tree.Children) == 0 {
		return XSLTResult{}, nil
	}
	p := &parser{ctx: ctx}
	w := xsltSaver{p: p, props: props}
	w.out = xsltOutputEncoder{p: p, id: xsltOutputEncoding(props.Encoding)}
	if w.out.id == -1 {
		// The memory saver allocates an unencoded buffer when handler lookup is
		// genuinely absent; declaration spelling is nevertheless preserved.
		w.out.id = 6
	}
	method := props.Method.Local
	if props.Method.URI != "" {
		return XSLTResult{}, fmt.Errorf("unresolved XSLT output method {%s}%s", props.Method.URI, method)
	}
	if !props.MethodSet {
		method = "xml"
		for _, n := range tree.Children {
			if p.err = ctx.Err(); p.err != nil {
				return XSLTResult{}, p.err
			}
			if n == nil {
				return XSLTResult{}, fmt.Errorf("nil XSLT result node")
			}
			if n.Kind == xsltResultText && !w.blank(n.Value) {
				break
			}
			if n.Kind == xsltResultElement {
				if n.Name.URI == "" && strings.EqualFold(n.Name.Local, "html") {
					method = "html"
				}
				break
			}
		}
	}
	switch method {
	case "xml", "":
	case "html":
		w.html = true
	case "text":
		w.text = true
	default:
		return XSLTResult{}, fmt.Errorf("unresolved XSLT output method %s", method)
	}
	if w.text && w.out.id == 6 && len(tree.Children) == 1 && tree.Children[0] != nil && tree.Children[0].Kind == xsltResultText {
		value := tree.Children[0].Value
		if !appendSize(0, len(value)) {
			return XSLTResult{}, appendSizeError(0, len(value))
		}
		for at := 0; at < len(value); at += min(4096, len(value)-at) {
			if err := ctx.Err(); err != nil {
				return XSLTResult{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return XSLTResult{}, err
		}
		// Strings are immutable even when the invocation-owned node is reassigned.
		return XSLTResult{Text: value}, nil
	}
	w.indent = props.Indent
	if w.html && !props.IndentSet {
		w.indent = true
	}
	w.finalLF = w.html || !w.text && (!props.IndentSet || props.Indent)
	if !w.html && !w.text && !props.OmitDeclaration {
		w.declaration()
	}
	if !w.text {
		w.doctype(tree)
	}
	if w.html {
		w.findMeta(tree)
	}
	stack := []xsltSaveFrame{{children: tree.Children, format: w.indent}}
	for len(stack) > 0 && p.err == nil {
		if p.err = ctx.Err(); p.err != nil {
			break
		}
		f := &stack[len(stack)-1]
		if f.next == len(f.children) {
			if f.node != nil {
				if !w.text {
					if w.html {
						if f.htmlBreak {
							w.out.write("\n")
						}
					} else if f.format {
						w.padding(len(stack) - 2)
					}
					w.out.write("</")
					w.name(f.node.Name)
					w.out.write(">")
				}
				w.restoreNamespaces(f.namespaceBase)
			}
			closed := f.node
			stack = stack[:len(stack)-1]
			if len(stack) > 0 && closed != nil {
				w.afterNode(&stack[len(stack)-1], closed)
			}
			continue
		}
		n := f.children[f.next]
		f.next++
		if n == nil {
			p.err = fmt.Errorf("nil XSLT result node")
			break
		}
		if w.text {
			if n.Kind == xsltResultText {
				w.out.write(n.Value)
			}
			if len(n.Children) > 0 {
				stack = checkedAppend(p, stack, xsltSaveFrame{node: n, children: n.Children})
			}
			continue
		}
		if !w.html && f.node != nil && f.format {
			w.padding(len(stack) - 1)
		}
		switch n.Kind {
		case xsltResultElement:
			if n.Name.Local == "" {
				p.err = fmt.Errorf("empty XSLT element name")
				break
			}
			w.out.write("<")
			w.name(n.Name)
			namespaceBase := w.writeNamespaces(n.Namespaces)
			for _, a := range n.Attributes {
				if !w.poll() {
					break
				}
				w.attribute(n, a)
			}
			injectMeta := n == w.metaHead && w.metaNode == nil
			if w.html && xsltHTMLVoid(n) {
				w.out.write(">")
				w.restoreNamespaces(namespaceBase)
				w.afterNode(f, n)
				continue
			}
			if len(n.Children) == 0 && !injectMeta {
				if w.html {
					w.out.write(">")
					if !xsltHTMLOmitEmptyEnd(n) {
						w.out.write("</")
						w.name(n.Name)
						w.out.write(">")
					}
				} else {
					w.out.write("/>")
				}
				w.restoreNamespaces(namespaceBase)
				w.afterNode(f, n)
				continue
			}
			w.out.write(">")
			format := f.format
			if !w.html {
				for _, child := range n.Children {
					if !w.poll() {
						break
					}
					if child != nil && child.Kind == xsltResultText {
						format = false
						break
					}
				}
				if format {
					w.out.write("\n")
				}
			}
			htmlBreak := false
			childCount := len(n.Children)
			if injectMeta {
				childCount++
			}
			if w.html && w.indent && xsltHTMLBlock(n) && !strings.HasPrefix(n.Name.Local, "p") && childCount > 1 {
				first, last := n.Children[0], n.Children[len(n.Children)-1]
				if injectMeta || first != nil && first.Kind != xsltResultText {
					w.out.write("\n")
				}
				htmlBreak = last != nil && last.Kind != xsltResultText
			}
			if injectMeta {
				w.meta()
				if len(n.Children) > 0 && n.Children[0] != nil && n.Children[0].Kind != xsltResultText && w.indent {
					w.out.write("\n")
				}
			}
			stack = checkedAppend(p, stack, xsltSaveFrame{node: n, children: n.Children, format: format, htmlBreak: htmlBreak, namespaceBase: namespaceBase})
		case xsltResultText:
			if !w.html && f.node != nil && props.CDATA[f.node.Name.key()] {
				w.out.write("<![CDATA[")
				brackets := 0
				w.cdataContent(n.Value, &brackets)
				for f.next < len(f.children) && f.children[f.next] != nil && f.children[f.next].Kind == xsltResultText {
					if !w.poll() {
						break
					}
					w.cdataContent(f.children[f.next].Value, &brackets)
					f.next++
				}
				w.out.write("]]>")
			} else if n.DisableEscaping || w.html && f.node != nil && (strings.EqualFold(f.node.Name.Local, "script") || strings.EqualFold(f.node.Name.Local, "style")) {
				w.out.write(n.Value)
			} else {
				w.escape(n.Value, false, w.html, false)
			}
		case xsltResultComment:
			w.out.write("<!--")
			w.out.write(n.Value)
			w.out.write("-->")
			w.afterNode(f, n)
		case xsltResultPI:
			w.out.write("<?")
			w.out.write(n.Name.Local)
			w.out.write(" ")
			w.out.write(n.Value)
			if w.html {
				w.out.write(">")
			} else {
				w.out.write("?>")
			}
			w.afterNode(f, n)
		default:
			p.err = fmt.Errorf("invalid XSLT result node kind %d", n.Kind)
		}
	}
	if w.finalLF {
		w.out.write("\n")
	}
	result := w.out.finish()
	if p.err == nil {
		p.err = ctx.Err()
	}
	if p.err != nil {
		return XSLTResult{}, p.err
	}
	return XSLTResult{Text: result}, nil
}
func (w *xsltSaver) name(n xsltName) {
	if n.Prefix != "" {
		w.out.write(n.Prefix)
		w.out.write(":")
	}
	w.out.write(n.Local)
}
func (w *xsltSaver) declaration() {
	version := w.props.Version
	if !w.props.VersionSet {
		version = "1.0"
	}
	w.out.write("<?xml version=\"")
	w.out.write(version)
	w.out.write("\"")
	if w.props.EncodingSet {
		w.out.write(" encoding=\"")
		w.out.write(w.props.Encoding)
		w.out.write("\"")
	}
	if w.props.StandaloneSet {
		w.out.write(" standalone=\"")
		w.out.write(w.props.Standalone)
		w.out.write("\"")
	}
	w.out.write("?>\n")
}
func (w *xsltSaver) padding(depth int) {
	// libxml's indentation buffer is sixty bytes, even for deeper trees.
	w.out.write(strings.Repeat(" ", min(depth*2, 60)))
}
func (w *xsltSaver) afterNode(f *xsltSaveFrame, n *xsltResultNode) {
	if !w.html {
		if f.node != nil && f.format {
			w.out.write("\n")
		} else if f.node == nil && n.Kind == xsltResultComment && f.next < len(f.children) && w.finalLF {
			w.out.write("\n")
		}
		return
	}
	if w.indent && xsltHTMLBlock(n) && f.next < len(f.children) && f.children[f.next] != nil && f.children[f.next].Kind != xsltResultText && f.node != nil && !strings.HasPrefix(f.node.Name.Local, "p") {
		w.out.write("\n")
	}
}
func (w *xsltSaver) reference(r rune, hex bool) {
	var buf [16]byte
	b := append(buf[:0], '&', '#')
	if hex {
		b = append(b, 'x')
		b = strconv.AppendInt(b, int64(r), 16)
		for i := 3; i < len(b); i++ {
			if b[i] >= 'a' && b[i] <= 'f' {
				b[i] -= 'a' - 'A'
			}
		}
	} else {
		b = strconv.AppendInt(b, int64(r), 10)
	}
	b = append(b, ';')
	w.out.write(string(b))
}
func (w *xsltSaver) escape(s string, attribute, html, quote bool) {
	start, nextPoll := 0, 0
	for at := 0; at < len(s) && w.p.err == nil; {
		if at >= nextPoll {
			w.p.err = w.p.ctx.Err()
			if w.p.err != nil {
				return
			}
			nextPoll = at + 1024
		}
		if html && attribute {
			terminator := ""
			if strings.HasPrefix(s[at:], "&{") {
				terminator = "}"
			} else if strings.HasPrefix(s[at:], "<!--") {
				terminator = "-->"
			}
			if terminator != "" {
				end := w.findTerminator(s[at:], terminator)
				if end >= 0 {
					end += at + len(terminator)
					w.out.write(s[start:at])
					w.literalAttribute(s[at:end], quote)
					at = end
					start = end
					continue
				}
			}
		}
		r, n := utf8.DecodeRuneInString(s[at:])
		replacement := ""
		switch r {
		case '&':
			replacement = "&amp;"
		case '<':
			replacement = "&lt;"
		case '>':
			replacement = "&gt;"
		case '"':
			if attribute && !html || quote {
				replacement = "&quot;"
			}
		case '\r':
			if !html {
				replacement = "&#13;"
			}
		case '\n':
			if attribute && !html {
				replacement = "&#10;"
			}
		case '\t':
			if attribute && !html {
				replacement = "&#9;"
			}
		}
		hex := !html && !w.props.EncodingSet && r >= 128
		if replacement != "" || hex {
			w.out.write(s[start:at])
			if hex {
				w.reference(r, true)
			} else {
				w.out.write(replacement)
			}
			start = at + n
		}
		at += n
	}
	w.out.write(s[start:])
}
func (w *xsltSaver) cdataContent(s string, brackets *int) {
	start := 0
	for at := range len(s) {
		if at%1024 == 0 && !w.poll() {
			return
		}
		c := s[at]
		if c == '>' && *brackets == 2 {
			w.out.write(s[start:at])
			w.out.write("]]><![CDATA[>")
			start = at + 1
		}
		if c == ']' {
			*brackets = min(*brackets+1, 2)
		} else {
			*brackets = 0
		}
	}
	w.out.write(s[start:])
}
func xsltHTMLVoid(n *xsltResultNode) bool {
	return n.Kind == xsltResultElement && n.Name.URI == "" && xsltWord("area base basefont br col frame hr img input isindex link meta param", n.Name.Local)
}
func xsltHTMLOmitEmptyEnd(n *xsltResultNode) bool {
	return n.Name.URI == "" && strings.EqualFold(n.Name.Local, "li")
}
func xsltHTMLBlock(n *xsltResultNode) bool {
	if n.Kind != xsltResultElement || n.Name.URI != "" {
		return false
	}
	return xsltWord("address area base blockquote body caption center col colgroup dd dir div dl dt fieldset form frame frameset h1 h2 h3 h4 h5 h6 head hr html isindex legend li link menu meta noframes noscript ol optgroup option p param pre style table tbody td tfoot th thead title tr ul", n.Name.Local)
}
func xsltWord(list, value string) bool {
	for list != "" {
		word, rest, ok := strings.Cut(list, " ")
		if strings.EqualFold(word, value) {
			return true
		}
		if !ok {
			break
		}
		list = rest
	}
	return false
}
func (w *xsltSaver) attribute(parent *xsltResultNode, a xsltResultAttribute) {
	w.out.write(" ")
	w.name(a.Name)
	if w.html && xsltWord("checked compact declare defer disabled ismap multiple nohref noresize noshade nowrap readonly selected", a.Name.Local) {
		return
	}
	w.out.write("=")
	if !w.html {
		w.out.write("\"")
		w.escape(a.Value, true, false, false)
		w.out.write("\"")
		return
	}
	value := a.Value
	if parent == w.metaNode && strings.EqualFold(a.Name.Local, "content") {
		value = w.metaContent
	}
	// htmlAttrDumpOutput quotes after entity expansion, selecting single quotes
	// only when they avoid escaping an embedded double quote.
	uri := a.Name.URI == "" && parent.Name.URI == "" && (xsltWord("href action src", a.Name.Local) || strings.EqualFold(a.Name.Local, "name") && strings.EqualFold(parent.Name.Local, "a"))
	single := !uri && w.findTerminator(value, "\"") >= 0 && w.findTerminator(value, "'") < 0
	if single {
		w.out.write("'")
	} else {
		w.out.write("\"")
	}
	if uri {
		w.uriAttribute(value)
	} else {
		w.escape(value, true, true, !single)
	}
	if single {
		w.out.write("'")
	} else {
		w.out.write("\"")
	}
}
func (w *xsltSaver) uriAttribute(value string) {
	value = strings.TrimLeft(value, " \t\r\n")
	const hex = "0123456789ABCDEF"
	for at := 0; at < len(value) && w.p.err == nil; at++ {
		if at%1024 == 0 {
			w.p.err = w.p.ctx.Err()
			if w.p.err != nil {
				return
			}
		}
		c := value[at]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.!~*'()@/:=?;#%&,+<>", rune(c)) {
			switch c {
			case '&':
				if at+1 < len(value) && value[at+1] == '{' {
					w.out.write("&")
				} else {
					w.out.write("&amp;")
				}
			case '<':
				w.out.write("&lt;")
			case '>':
				w.out.write("&gt;")
			default:
				w.out.write(value[at : at+1])
			}
		} else {
			var b = [3]byte{'%', hex[c>>4], hex[c&15]}
			w.out.write(string(b[:]))
		}
	}
}
func (w *xsltSaver) doctype(tree *xsltResultTree) {
	pub, sys := w.props.DoctypePublic, w.props.DoctypeSystem
	pubSet, sysSet := w.props.DoctypePublicSet, w.props.DoctypeSystemSet
	if w.html && !pubSet && !sysSet && w.props.VersionSet {
		pub, sys = xsltHTMLDoctype(w.props.Version)
		pubSet, sysSet = pub != "", sys != ""
		if !pubSet && !sysSet && w.props.MethodSet {
			pub, sys = "-//W3C//DTD HTML 4.0 Transitional//EN", "http://www.w3.org/TR/REC-html40/loose.dtd"
			pubSet, sysSet = true, true
		}
	}
	if !pubSet && !sysSet {
		return
	}
	var root *xsltResultNode
	for _, n := range tree.Children {
		if !w.poll() {
			return
		}
		if n != nil && n.Kind == xsltResultElement {
			root = n
			break
		}
	}
	if root == nil && !w.html {
		return
	}
	w.out.write("<!DOCTYPE ")
	if w.html {
		w.out.write("html")
	} else {
		w.name(root.Name)
	}
	if pubSet {
		w.out.write(" PUBLIC ")
		w.quoted(pub)
		if sysSet || !w.html {
			w.out.write(" ")
			w.quoted(sys)
		}
	} else if sysSet {
		w.out.write(" SYSTEM ")
		w.quoted(sys)
	}
	w.out.write(">")
	if w.html || w.finalLF {
		w.out.write("\n")
	}
}
func (w *xsltSaver) quoted(s string) {
	single := w.findTerminator(s, "\"") >= 0 && w.findTerminator(s, "'") < 0
	if single {
		w.out.write("'")
		w.out.write(s)
		w.out.write("'")
	} else {
		w.out.write("\"")
		w.literalAttribute(s, true)
		w.out.write("\"")
	}
}
func xsltHTMLDoctype(version string) (string, string) {
	switch strings.ToLower(version) {
	case "5":
		return "", "about:legacy-compat"
	case "3.2":
		return "-//W3C//DTD HTML 3.2//EN", ""
	case "4.01", "4.01trans":
		return "-//W3C//DTD HTML 4.01 Transitional//EN", "http://www.w3.org/TR/1999/REC-html401-19991224/loose.dtd"
	case "4.01strict":
		return "-//W3C//DTD HTML 4.01//EN", "http://www.w3.org/TR/1999/REC-html401-19991224/strict.dtd"
	case "4.01frame":
		return "-//W3C//DTD HTML 4.01 Frameset//EN", "http://www.w3.org/TR/1999/REC-html401-19991224/frameset.dtd"
	case "4.0", "4.0trans":
		return "-//W3C//DTD HTML 4.01 Transitional//EN", "http://www.w3.org/TR/html4/loose.dtd"
	case "4.0strict":
		return "-//W3C//DTD HTML 4.01//EN", "http://www.w3.org/TR/html4/strict.dtd"
	case "4.0frame":
		return "-//W3C//DTD HTML 4.01 Frameset//EN", "http://www.w3.org/TR/html4/frameset.dtd"
	}
	return "", ""
}
func (w *xsltSaver) findMeta(tree *xsltResultTree) {
	encoding := w.props.Encoding
	if !w.props.EncodingSet {
		encoding = "UTF-8"
	}
	if strings.EqualFold(encoding, "html") {
		return
	}
	// Match htmlSetMetaEncoding's shallow document/html/head search, rather than
	// injecting META into every nested head or traversing foreign namespaces.
	nodes := tree.Children
	for range 3 {
		var found *xsltResultNode
		for _, n := range nodes {
			if !w.poll() {
				return
			}
			if n == nil || n.Kind != xsltResultElement {
				continue
			}
			if strings.EqualFold(n.Name.Local, "html") || strings.EqualFold(n.Name.Local, "head") || strings.EqualFold(n.Name.Local, "meta") {
				found = n
				break
			}
		}
		if found == nil {
			return
		}
		if strings.EqualFold(found.Name.Local, "head") {
			w.metaHead = found
			nodes = found.Children
			break
		}
		if strings.EqualFold(found.Name.Local, "meta") {
			nodes = []*xsltResultNode{found}
			break
		}
		nodes = found.Children
	}
	for _, n := range nodes {
		if !w.poll() {
			return
		}
		if n == nil || n.Kind != xsltResultElement || !strings.EqualFold(n.Name.Local, "meta") {
			continue
		}
		http, content := false, ""
		for _, a := range n.Attributes {
			if !w.poll() {
				return
			}
			if strings.EqualFold(a.Name.Local, "http-equiv") && strings.EqualFold(a.Value, "Content-Type") {
				http = true
			}
			if strings.EqualFold(a.Name.Local, "content") {
				content = a.Value
			}
		}
		if http && content != "" {
			w.metaNode = n
			if w.containsFold(content, encoding) {
				w.metaContent = content
			} else {
				w.metaContent = w.metaCharset()
			}
			return
		}
	}
}
func (w *xsltSaver) meta() {
	w.out.write("<meta http-equiv=\"Content-Type\" content=\"")
	w.escape(w.metaCharset(), true, true, true)
	w.out.write("\">")
}

func (w *xsltSaver) writeNamespaces(declarations []xsltNamespace) int {
	base := len(w.namespaceUndo)
	for _, ns := range declarations {
		if !w.poll() {
			break
		}
		old := w.namespaces[ns.Prefix]
		if ns.Prefix == "xml" && old == "" {
			old = "http://www.w3.org/XML/1998/namespace"
		}
		if old == ns.URI {
			continue
		}
		w.namespaceUndo = checkedAppend(w.p, w.namespaceUndo, xsltNamespace{Prefix: ns.Prefix, URI: old})
		if w.p.err != nil {
			break
		}
		if _, exists := w.namespaces[ns.Prefix]; !exists && !w.p.collectionSize(len(w.namespaces), 48) {
			break
		}
		if w.namespaces == nil {
			w.namespaces = make(map[string]string)
		}
		w.namespaces[ns.Prefix] = ns.URI
		w.out.write(" xmlns")
		if ns.Prefix != "" {
			w.out.write(":")
			w.out.write(ns.Prefix)
		}
		// xmlNsDumpOutput uses xmlBufWriteQuotedString, not ordinary attribute
		// escaping: only quote delimiters change, while ampersands and UTF8
		// remain literal and still pass through the selected output encoder.
		w.out.write("=")
		w.quoted(ns.URI)
	}
	return base
}
func (w *xsltSaver) restoreNamespaces(base int) {
	for i := len(w.namespaceUndo) - 1; i >= base; i-- {
		if !w.poll() {
			return
		}
		ns := w.namespaceUndo[i]
		if ns.URI == "" {
			delete(w.namespaces, ns.Prefix)
		} else {
			w.namespaces[ns.Prefix] = ns.URI
		}
	}
	w.namespaceUndo = w.namespaceUndo[:base]
}

func (w *xsltSaver) findTerminator(s, needle string) int {
	for at := 0; at < len(s); {
		if !w.poll() {
			return -1
		}
		end := at + min(4096, len(s)-at)
		if i := strings.Index(s[at:end], needle); i >= 0 {
			return at + i
		}
		if end == len(s) {
			break
		}
		at = end - len(needle) + 1
	}
	return -1
}
func (w *xsltSaver) literalAttribute(s string, quote bool) {
	start := 0
	for at := range len(s) {
		if at%1024 == 0 && !w.poll() {
			return
		}
		if quote && s[at] == '"' {
			w.out.write(s[start:at])
			w.out.write("&quot;")
			start = at + 1
		}
	}
	w.out.write(s[start:])
}

func (w *xsltSaver) poll() bool {
	if w.p.err == nil {
		w.p.err = w.p.ctx.Err()
	}
	return w.p.err == nil
}
func (w *xsltSaver) blank(s string) bool {
	for at := range len(s) {
		if at%1024 == 0 && !w.poll() {
			return false
		}
		if s[at] != ' ' && s[at] != '\t' && s[at] != '\r' && s[at] != '\n' {
			return false
		}
	}
	return true
}

func (w *xsltSaver) metaCharset() string {
	encoding := w.props.Encoding
	if !w.props.EncodingSet {
		encoding = "UTF-8"
	}
	// htmlSetMetaEncoding owns a 100-byte NUL-terminated content buffer.
	return "text/html; charset=" + encoding[:min(80, len(encoding))]
}
func (w *xsltSaver) containsFold(s, needle string) bool {
	if needle == "" {
		return true
	}
	for at := 0; at <= len(s)-len(needle); at++ {
		if at%1024 == 0 && !w.poll() {
			return false
		}
		match := true
		for i := range len(needle) {
			if i%1024 == 0 && !w.poll() {
				return false
			}
			a, b := s[at+i], needle[i]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
