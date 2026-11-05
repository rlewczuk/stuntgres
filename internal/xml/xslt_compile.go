package xml

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type xsltProgram struct {
	style         *xpathTree
	root          *xsltInstruction
	templates     []*xsltTemplate
	dispatch      map[xsltDispatchKey][]xsltTemplateBranch
	named         map[string]*xsltTemplate
	globals       map[string]*xsltInstruction
	globalOrder   []string
	attributeSets map[string][]*xsltInstruction
	keys          map[string][]xsltKey
	decimals      map[string]xsltDecimalFormat
	spaces        []xsltSpaceRule
	aliases       map[string]string
	output        xsltOutputProperties
}
type xsltTemplate struct {
	instruction       *xsltInstruction
	patterns          []xsltPattern
	name, mode        string
	priority          float64
	explicitPriority  bool
	precedence, order int
}
type xsltAVTPart struct {
	text       string
	expression *xpathExpr
}
type xsltAVT []xsltAVTPart
type xsltInstruction struct {
	source                  *node
	op                      string
	namespaces              map[string]string
	namespaceOrder          []xsltNamespace
	excluded, extensions    map[string]bool
	children                []*xsltInstruction
	expressions             map[string]*xpathExpr
	avts                    map[string]xsltAVT
	name                    xsltName
	mode                    string
	forward                 bool
	baseURI                 string
	line                    int
	numberCount, numberFrom []xsltPattern
}
type xsltKey struct {
	pattern     []xsltPattern
	use         *xpathExpr
	instruction *xsltInstruction
}
type xsltSpaceRule struct {
	name     xsltName
	strip    bool
	priority float64
	order    int
}
type xsltCompiler struct {
	p          parser
	program    *xsltProgram
	stylesheet bool
}

func xsltFailure(stage XSLTStage, detail string) error {
	return &XSLTError{Stage: stage, Detail: detail}
}
func (c *xsltCompiler) fail(detail string) {
	if c.p.err == nil {
		c.p.err = xsltFailure(XSLTCompile, detail)
	}
}
func xsltAttr(n *node, name string) (string, bool) {
	for _, a := range n.attributes {
		if a.uri == "" && a.local == name {
			return a.value, true
		}
	}
	return "", false
}
func xsltAttribute(i *xsltInstruction, name string) string {
	v, _ := xsltAttr(i.source, name)
	return v
}
func xsltQName(s string, ns map[string]string, defaultNS bool) (xsltName, bool) {
	p, l, valid := xsltLexicalQName(s)
	if !valid {
		return xsltName{}, false
	}
	uri := ""
	if p == "xml" {
		uri = xmlNamespaceURI
	} else if p != "" || defaultNS {
		var ok bool
		uri, ok = ns[p]
		if p != "" && !ok {
			return xsltName{}, false
		}
	}
	return xsltName{Prefix: p, Local: l, URI: uri}, true
}
func xsltNCName(s string) bool {
	first := true
	for _, r := range s {
		if r == ':' || first && !nameStart(r) || !first && !nameChar(r) {
			return false
		}
		first = false
	}
	return !first
}
func xsltCloneBindings(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
func xsltResolveBase(base, reference string) string {
	u, e := url.Parse(reference)
	if e != nil {
		return reference
	}
	b, e := url.Parse(base)
	if e != nil {
		return reference
	}
	return b.ResolveReference(u).String()
}
func compileXSLT(ctx context.Context, d *document) (*xsltProgram, error) {
	p := &xsltProgram{named: make(map[string]*xsltTemplate), globals: make(map[string]*xsltInstruction), attributeSets: make(map[string][]*xsltInstruction), keys: make(map[string][]xsltKey), decimals: map[string]xsltDecimalFormat{"\x00": xsltDefaultDecimalFormat()}, aliases: make(map[string]string)}
	c := xsltCompiler{p: parser{ctx: ctx}, program: p}
	var root *node
	for _, n := range d.nodes {
		if n.kind == elementNode {
			root = n
			break
		}
	}
	if root == nil {
		return nil, xsltFailure(XSLTCompile, "")
	}
	c.preprocessNamespaces(root)
	c.stylesheet = root.uri == xsltNamespaceURI
	p.root = c.instruction(root, nil, nil, nil, nil, d.baseURI, false, false, 0)
	if c.p.err != nil {
		return nil, c.p.err
	}
	if root.uri != xsltNamespaceURI {
		version := false
		for _, a := range root.attributes {
			if a.uri == xsltNamespaceURI && a.local == "version" {
				version = true
			}
		}
		if !version {
			return nil, xsltFailure(XSLTCompile, "")
		}
		p.templates = append(p.templates, &xsltTemplate{instruction: &xsltInstruction{op: "template", children: []*xsltInstruction{p.root}}, patterns: []xsltPattern{{root: true, priority: 0.5}}})
	} else {
		if root.local != "stylesheet" && root.local != "transform" {
			return nil, xsltFailure(XSLTCompile, "")
		}
		if _, ok := xsltAttr(root, "version"); !ok {
			return nil, xsltFailure(XSLTCompile, "")
		}
		for order, i := range p.root.children {
			if c.p.err != nil {
				break
			}
			if i.source.kind != elementNode {
				if i.op == "text" && strings.TrimSpace(i.source.value) != "" {
					c.fail("")
				}
				continue
			}
			if i.source.uri != xsltNamespaceURI {
				if i.source.uri == "" {
					c.fail("")
				}
				continue
			}
			switch i.op {
			case "template":
				t := &xsltTemplate{instruction: i, order: order}
				if s, ok := xsltAttr(i.source, "name"); ok {
					n, valid := xsltQName(s, i.namespaces, false)
					if !valid {
						c.fail("")
						break
					}
					t.name = n.key()
					if p.named[t.name] != nil {
						c.fail("")
						break
					}
					p.named[t.name] = t
				}
				if s, ok := xsltAttr(i.source, "mode"); ok {
					n, valid := xsltQName(s, i.namespaces, false)
					if !valid {
						c.fail("")
						break
					}
					t.mode = n.key()
				}
				if s, ok := xsltAttr(i.source, "priority"); ok {
					v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
					if err != nil {
						c.fail("")
						break
					}
					t.priority, t.explicitPriority = v, true
				}
				if s, ok := xsltAttr(i.source, "match"); ok {
					t.patterns, c.p.err = compileXSLTPattern(ctx, s, i.namespaces, false)
				}
				if t.name == "" && len(t.patterns) == 0 {
					c.fail("")
				}
				p.templates = checkedAppend(&c.p, p.templates, t)
			case "variable", "param":
				if _, exists := p.globals[i.name.key()]; exists {
					c.fail("")
					break
				}
				p.globals[i.name.key()] = i
				p.globalOrder = checkedAppend(&c.p, p.globalOrder, i.name.key())
			case "output":
				c.output(i)
			case "key":
				match, ok := xsltAttr(i.source, "match")
				use, hasUse := i.expressions["use"]
				if !ok || !hasUse {
					c.fail("")
					break
				}
				patterns, err := compileXSLTPattern(ctx, match, i.namespaces, true)
				if err != nil {
					c.p.err = err
					break
				}
				if err := xsltCheckExpression(ctx, use, true, false, true); err != nil {
					c.p.err = err
					break
				}
				p.keys[i.name.key()] = append(p.keys[i.name.key()], xsltKey{pattern: patterns, use: use, instruction: i})
			case "attribute-set":
				p.attributeSets[i.name.key()] = append(p.attributeSets[i.name.key()], i)
			case "decimal-format":
				c.decimal(i)
			case "strip-space", "preserve-space":
				for _, s := range strings.Fields(xsltAttribute(i, "elements")) {
					n, priority, ok := xsltNameTest(s, i.namespaces)
					if !ok {
						c.fail("")
						break
					}
					p.spaces = checkedAppend(&c.p, p.spaces, xsltSpaceRule{name: n, strip: i.op == "strip-space", priority: priority, order: order})
				}
			case "namespace-alias":
				a, b := xsltAttribute(i, "stylesheet-prefix"), xsltAttribute(i, "result-prefix")
				if a == "#default" {
					a = ""
				}
				if b == "#default" {
					b = ""
				}
				au, ok := i.namespaces[a]
				bu, bok := i.namespaces[b]
				if a != "" && !ok || b != "" && !bok {
					c.fail("")
					break
				}
				// The released alias table maps URIs, not lexical prefixes.
				// An absent result default removes the source namespace.
				if _, exists := p.aliases[au]; !exists {
					p.aliases[au] = bu
				}
			case "include", "import":
				c.externalModule(i)
			default:
				if !i.forward {
					c.fail("")
				}
			}
		}
	}
	if c.p.err != nil {
		return nil, c.p.err
	}
	if err := c.resolveAttributeSets(); err != nil {
		return nil, err
	}
	if err := p.indexTemplates(ctx); err != nil {
		return nil, err
	}
	if err := p.preprocessStyleTree(ctx); err != nil {
		return nil, err
	}
	f, err := newXPathTree(ctx, d)
	if err != nil {
		return nil, err
	}
	p.style = f.tree
	p.style.ordinal = 1
	return p, nil
}
func (c *xsltCompiler) instruction(n *node, parentNS map[string]string, parentOrder []xsltNamespace, parentExcluded, parentExtensions map[string]bool, base string, forward, preserveSpace bool, depth int) *xsltInstruction {
	if c.p.err != nil {
		return nil
	}
	if c.p.err = c.p.ctx.Err(); c.p.err != nil {
		return nil
	}
	if depth > 256 {
		c.fail("")
		return nil
	}
	if n.kind != elementNode {
		op := "ignored"
		if n.kind == textNode || n.kind == cdataNode {
			op = "text"
		}
		return &xsltInstruction{source: n, op: op, line: n.line, baseURI: base}
	}
	i := &xsltInstruction{source: n, op: n.local, namespaces: xsltCloneBindings(parentNS), excluded: make(map[string]bool), extensions: make(map[string]bool), expressions: make(map[string]*xpathExpr), avts: make(map[string]xsltAVT), forward: forward, line: n.line}
	for k, v := range parentExcluded {
		i.excluded[k] = v
	}
	for k, v := range parentExtensions {
		i.extensions[k] = v
	}
	for _, a := range n.namespaces {
		prefix := a.local
		if a.name == "xmlns" {
			prefix = ""
		}
		// Relocation may leave several declarations with the same prefix on
		// the root. Namespace lookup uses the first, nearest declaration.
		shadowed := false
		for _, local := range i.namespaceOrder {
			if local.Prefix == prefix {
				shadowed = true
				break
			}
		}
		if shadowed {
			continue
		}
		i.namespaces[prefix] = a.value
		i.namespaceOrder = checkedAppend(&c.p, i.namespaceOrder, xsltNamespace{prefix, a.value})
	}
	for _, ns := range parentOrder {
		found := false
		for _, local := range i.namespaceOrder {
			if local.Prefix == ns.Prefix {
				found = true
				break
			}
		}
		if !found {
			i.namespaceOrder = checkedAppend(&c.p, i.namespaceOrder, ns)
		}
	}
	i.namespaces["xml"] = xmlNamespaceURI
	for _, a := range n.attributes {
		if a.uri == xmlNamespaceURI && a.local == "base" {
			base = xsltResolveBase(base, a.value)
		}
		if a.uri == xmlNamespaceURI && a.local == "space" {
			preserveSpace = a.value == "preserve"
		}
		if (n.uri == xsltNamespaceURI && a.uri == "") || a.uri == xsltNamespaceURI {
			switch a.local {
			case "version":
				i.forward = a.value != "1.0"
			case "exclude-result-prefixes", "extension-element-prefixes":
				for _, prefix := range strings.Fields(a.value) {
					if prefix == "#default" {
						prefix = ""
					}
					uri, ok := i.namespaces[prefix]
					if !ok {
						// Unbound exclusion prefixes are generic compiler warnings,
						// not structured XPath callbacks or fatal stylesheet errors.
						if a.local != "exclude-result-prefixes" {
							c.fail("")
						}
						continue
					}
					if a.local == "exclude-result-prefixes" {
						if depth == 0 {
							i.excluded[uri] = true
						}
					} else {
						i.extensions[uri] = true
					}
				}
			}
		}
	}
	i.baseURI = base
	n.baseURI = base
	if c.stylesheet && depth == 1 && n.uri != xsltNamespaceURI {
		return i
	}
	if n.uri != xsltNamespaceURI {
		if i.extensions[n.uri] {
			i.op = "extension"
		} else {
			i.op = "literal"
			i.name = xsltName{Prefix: n.prefix, Local: n.local, URI: n.uri}
		}
	}
	nameRequired := i.op == "variable" || i.op == "param" || i.op == "with-param" || i.op == "attribute-set" || i.op == "key" || i.op == "call-template"
	if nameRequired {
		s, ok := xsltAttr(n, "name")
		name, valid := xsltQName(s, i.namespaces, false)
		if !ok || !valid {
			c.fail("")
		} else {
			i.name = name
		}
	}
	if i.op == "apply-templates" {
		if s, ok := xsltAttr(n, "mode"); ok {
			mode, valid := xsltQName(s, i.namespaces, false)
			if !valid {
				c.fail("")
			} else {
				i.mode = mode.key()
			}
		}
	}
	exprNames := map[string]bool{}
	switch i.op {
	case "value-of", "copy-of", "for-each":
		exprNames["select"] = true
	case "if", "when":
		exprNames["test"] = true
	case "variable", "param", "with-param", "apply-templates", "sort":
		exprNames["select"] = false
	case "key":
		exprNames["use"] = true
	case "number":
		exprNames["value"] = false
	}
	for name, required := range exprNames {
		if value, ok := xsltAttr(n, name); ok {
			e, err := compileXPath(c.p.ctx, value)
			if err != nil {
				c.p.err = err
			} else {
				i.expressions[name] = e
			}
		} else if required {
			c.fail("")
		}
	}
	for _, a := range n.attributes {
		avt := false
		if i.op == "literal" {
			avt = a.uri != xsltNamespaceURI
		} else if a.uri == "" {
			switch i.op {
			case "element", "attribute":
				avt = a.local == "name" || a.local == "namespace"
			case "processing-instruction":
				avt = a.local == "name"
			case "sort":
				avt = a.local != "select"
			case "number":
				avt = a.local == "format" || a.local == "lang" || a.local == "letter-value" || a.local == "grouping-separator" || a.local == "grouping-size"
			}
		}
		if avt {
			parts, err := compileXSLTAVT(c.p.ctx, a.value)
			if err != nil {
				c.p.err = err
			} else {
				i.avts[a.uri+"\x00"+a.local] = parts
			}
		}
	}
	for _, child := range n.children {
		ci := c.instruction(child, i.namespaces, i.namespaceOrder, i.excluded, i.extensions, base, i.forward, preserveSpace, depth+1)
		if c.p.err != nil {
			break
		}
		if ci.op == "ignored" {
			continue
		}
		if (child.kind == textNode || child.kind == cdataNode) && i.op != "text" && strings.Trim(child.value, " \t\r\n") == "" {
			if !preserveSpace {
				continue
			}
		}
		i.children = checkedAppend(&c.p, i.children, ci)
	}
	if i.op == "text" {
		for _, child := range n.children {
			if child.kind != textNode && child.kind != cdataNode && child.kind != commentNode {
				c.fail("")
			}
		}
	}
	if (i.op == "variable" || i.op == "param" || i.op == "with-param") && i.expressions["select"] != nil && len(i.children) > 0 {
		c.fail("")
	}
	if i.op == "number" {
		if s, ok := xsltAttr(n, "count"); ok {
			var err error
			i.numberCount, err = compileXSLTNumberPattern(c.p.ctx, s, i.namespaces)
			if err != nil {
				c.p.err = err
			}
		}
		if s, ok := xsltAttr(n, "from"); ok {
			var err error
			i.numberFrom, err = compileXSLTNumberPattern(c.p.ctx, s, i.namespaces)
			if err != nil {
				c.p.err = err
			}
		}
	}
	c.validateXSLTChildren(i)
	return i
}
func compileXSLTAVT(ctx context.Context, s string) (xsltAVT, error) {
	var out xsltAVT
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			out = append(out, xsltAVTPart{text: literal.String()})
			literal.Reset()
		}
	}
	for at := 0; at < len(s); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ch := s[at]
		if (ch == '{' || ch == '}') && at+1 < len(s) && s[at+1] == ch {
			literal.WriteByte(ch)
			at += 2
			continue
		}
		if ch == '}' {
			return nil, xsltFailure(XSLTCompile, "")
		}
		if ch != '{' {
			literal.WriteByte(ch)
			at++
			continue
		}
		flush()
		start := at + 1
		at++
		quote := byte(0)
		for at < len(s) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ch = s[at]
			if quote != 0 {
				if ch == quote {
					quote = 0
				}
			} else if ch == '\'' || ch == '"' {
				quote = ch
			} else if ch == '}' {
				break
			} else if ch == '{' {
				return nil, xsltFailure(XSLTCompile, "")
			}
			at++
		}
		if at == len(s) {
			return nil, xsltFailure(XSLTCompile, "")
		}
		e, err := compileXPath(ctx, s[start:at])
		if err != nil {
			return nil, err
		}
		out = append(out, xsltAVTPart{expression: e})
		at++
	}
	flush()
	return out, nil
}
func xsltCheckExpression(ctx context.Context, e *xpathExpr, variables, current, key bool) error {
	p := parser{ctx: ctx}
	stack := []*xpathExpr{e}
	for len(stack) > 0 && p.err == nil {
		p.err = ctx.Err()
		if p.err != nil {
			break
		}
		last := len(stack) - 1
		expr := stack[last]
		stack = stack[:last]
		if expr == nil {
			continue
		}
		if variables && expr.op == "variable" || expr.op == "call" && (current && expr.name == "current" || key && expr.name == "key") {
			return xsltFailure(XSLTCompile, "")
		}
		for _, a := range expr.args {
			stack = checkedAppend(&p, stack, a)
		}
		for _, a := range expr.predicates {
			stack = checkedAppend(&p, stack, a)
		}
		for _, s := range expr.steps {
			for _, a := range s.predicates {
				stack = checkedAppend(&p, stack, a)
			}
		}
	}
	return p.err
}
func xsltNameTest(s string, ns map[string]string) (xsltName, float64, bool) {
	if s == "*" {
		return xsltName{Local: "*", URI: "*"}, -0.5, true
	}
	if prefix, ok := strings.CutSuffix(s, ":*"); ok {
		uri, found := ns[prefix]
		return xsltName{Local: "*", URI: uri}, -0.25, found
	}
	n, ok := xsltQName(s, ns, false)
	return n, 0, ok
}
func (c *xsltCompiler) output(i *xsltInstruction) {
	p := &c.program.output
	for _, a := range i.source.attributes {
		if a.uri != "" {
			continue
		}
		switch a.local {
		case "method":
			n, ok := xsltQName(a.value, i.namespaces, false)
			if !ok {
				c.fail("")
				continue
			}
			if n.URI == "" && n.Local != "xml" && n.Local != "html" && n.Local != "text" {
				p.Method, p.MethodSet = xsltName{}, false
			} else {
				p.Method, p.MethodSet = n, true
			}
		case "version":
			p.Version, p.VersionSet = a.value, true
		case "encoding":
			p.Encoding, p.EncodingSet = a.value, true
		case "standalone":
			if a.value != "yes" && a.value != "no" {
				c.fail("")
			}
			p.Standalone, p.StandaloneSet = a.value, true
		case "doctype-public":
			p.DoctypePublic, p.DoctypePublicSet = a.value, true
		case "doctype-system":
			p.DoctypeSystem, p.DoctypeSystemSet = a.value, true
		case "media-type":
			p.MediaType, p.MediaTypeSet = a.value, true
		case "omit-xml-declaration":
			if a.value != "yes" && a.value != "no" {
				c.fail("")
			}
			p.OmitDeclaration, p.OmitDeclarationSet = a.value == "yes", true
		case "indent":
			if a.value != "yes" && a.value != "no" {
				c.fail("")
			}
			p.Indent, p.IndentSet = a.value == "yes", true
		case "cdata-section-elements":
			if p.CDATA == nil {
				p.CDATA = make(map[string]bool)
			}
			for _, s := range strings.Fields(a.value) {
				n, ok := xsltQName(s, i.namespaces, true)
				if !ok {
					c.fail("")
					break
				}
				p.CDATA[n.key()] = true
			}
		}
	}
}
