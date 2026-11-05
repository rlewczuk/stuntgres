package xml

import (
	"context"
	"errors"
	"strings"
)

type xsltRuntime struct {
	program                 *xsltProgram
	frame                   *xpathFrame
	source                  *xpathTree
	user, globals           map[string]xpathValue
	globalState             map[string]uint8
	registrations           []*xsltDocumentInfo
	keysBuilding            map[string]bool
	prefixCounter, runDepth int
	failed                  bool // transform ERROR is distinct from STOPPED/XPath/ordinary failure
	focus                   xsltFocus
}

func (r *xsltRuntime) fail() { r.failed = true }

type xsltDocumentInfo struct {
	tree *xpathTree
	keys map[string]map[string][]*xpathNode
}
type xsltEnvironment struct {
	runtime     *xsltRuntime
	scope       *xsltScope
	current     *xpathNode
	instruction *xsltInstruction
	userValues  bool
}
type xsltContext struct {
	node                  *xpathNode
	position, size, depth int
	scope                 *xsltScope
	target                xsltTarget
	mode                  string
	rule                  *xsltTemplate
}
type xsltTask struct {
	kind        uint8
	instruction *xsltInstruction
	sequence    []*xsltInstruction
	index       int
	context     xsltContext
	params      map[string]xpathValue
	fragment    *xsltResultTree
	name        xsltName
}

const (
	xsltSequenceTask uint8 = iota
	xsltInstructionTask
	xsltApplyTask
	xsltFinishBindingTask
	xsltFinishContentTask
	xsltBuiltinTask
	xsltRestoreFocusTask
)

func applyXSLT(ctx context.Context, program *xsltProgram, source *document, pairs []xsltParamPair) (*xsltResultTree, error) {
	f, err := newXPathTree(ctx, source)
	if err != nil {
		return nil, err
	}
	r := &xsltRuntime{program: program, frame: f, source: f.tree, user: make(map[string]xpathValue), globals: make(map[string]xpathValue), globalState: make(map[string]uint8), keysBuilding: make(map[string]bool)}
	r.registrations = []*xsltDocumentInfo{{tree: f.tree, keys: make(map[string]map[string][]*xpathNode)}}
	f.environment = &xsltEnvironment{runtime: r, current: f.node}
	r.stripWhitespace()
	if err := r.userParameters(pairs); err != nil {
		return nil, err
	}
	// Each global select has its own XPath evaluation. The released eager
	// global scan continues after a failed select; every NULL variable lookup
	// in its callers reports its own Undefined variable callback.
	var diagnostics strings.Builder
	globalFailure := false
	for _, key := range program.globalOrder {
		_, _ = r.globalValue(key)
		if f.p.err != nil {
			var failure *XPathError
			if !errors.As(f.p.err, &failure) {
				return nil, f.p.err
			}
			f.p.err = nil
			globalFailure = true
			if failure.Detail != "" {
				if diagnostics.Len() != 0 {
					f.p.builderByte(&diagnostics, '\n')
				}
				f.p.appendValue(&diagnostics, failure.Detail)
				if f.p.err != nil {
					return nil, f.p.err
				}
			}
		}
	}
	if globalFailure {
		return nil, xsltFailure(XSLTApply, diagnostics.String())
	}
	if program.output.MethodSet && program.output.Method.URI != "" {
		return nil, xsltFailure(XSLTApply, "")
	}
	tree := &xsltResultTree{}
	state := xsltContext{node: f.tree.root, position: 1, size: 1, scope: xsltNewScope(nil), target: xsltTarget{tree: tree}}
	err = r.run([]xsltTask{{kind: xsltApplyTask, context: state}})
	if err != nil {
		return nil, err
	}
	if r.failed {
		return nil, xsltFailure(XSLTApply, "")
	}
	return tree, nil
}
func (r *xsltRuntime) stripWhitespace() {
	f := r.frame
	for _, n := range r.source.ordered {
		if !f.poll() {
			return
		}
		if n.kind != xpathElement {
			continue
		}
		strip, priority, order := false, -2.0, -1
		for _, rule := range r.program.spaces {
			name := rule.name
			if (name.URI == "*" || name.URI == n.uri) && (name.Local == "*" || name.Local == n.local) && (rule.priority > priority || rule.priority == priority && rule.order > order) {
				strip, priority, order = rule.strip, rule.priority, rule.order
			}
		}
		if !strip {
			continue
		}
		out := n.children[:0]
		for _, child := range n.children {
			if child.kind == xpathText && strings.Trim(child.value, " \t\r\n") == "" {
				child.parent = nil
				continue
			}
			out = append(out, child)
		}
		n.children = out
	}
	out := r.source.ordered[:0]
	for _, n := range r.source.ordered {
		if n.kind == xpathText && n.parent == nil {
			continue
		}
		n.order = len(out)
		out = append(out, n)
	}
	r.source.ordered = out
}
func (e *xsltEnvironment) variable(f *xpathFrame, lexical string) xpathValue {
	name, ok := xsltQName(lexical, f.bindings, false)
	if !ok {
		f.runtimeError("")
		return xpathValue{}
	}
	if e.userValues {
		if value, exists := e.runtime.user[name.key()]; exists {
			return value
		}
	}
	if !e.userValues {
		if value, exists := e.scope.lookup(name.key()); exists {
			return value
		}
		if value, exists := e.runtime.user[name.key()]; exists {
			return value
		}
		if _, exists := e.runtime.program.globals[name.key()]; exists {
			value, valid := e.runtime.globalValue(name.key())
			if valid {
				return value
			}
			// Nested XPath evaluations have separate error contexts in libxml.
			// Preserve their callbacks, then report this caller's failed lookup.
			var failure *XPathError
			if errors.As(f.p.err, &failure) {
				detail := failure.Detail
				f.p.err = nil
				if detail != "" {
					detail = f.p.joinMessage(detail, "\n", "Undefined variable")
				} else {
					detail = "Undefined variable"
				}
				if f.p.err == nil {
					f.runtimeError(detail)
				}
				return xpathValue{}
			}
		}
	}
	f.runtimeError("Undefined variable")
	return xpathValue{}
}
func (r *xsltRuntime) globalValue(key string) (xpathValue, bool) {
	if value, ok := r.globals[key]; ok {
		return value, true
	}
	f := r.frame
	if r.globalState[key] == 1 {
		// A recursive definition returns no XPath object. The variable lookup
		// instruction, not the cycle detector, emits the structured callback.
		return xpathValue{}, false
	}
	i := r.program.globals[key]
	if i == nil {
		return xpathValue{}, false
	}
	r.globalState[key] = 1
	if i.op == "param" {
		if value, ok := r.user[key]; ok {
			r.globals[key] = value
			r.globalState[key] = 2
			return value, true
		}
	}
	value := r.instructionValue(i, xsltContext{node: r.source.root, position: 1, size: 1, scope: xsltNewScope(nil)})
	if f.p.err != nil {
		// Failed globals remain uncomputed, so a later eager scan entry can
		// evaluate its own dependency chain rather than reusing a fake value.
		r.globalState[key] = 0
		return xpathValue{}, false
	}
	r.globals[key] = value
	r.globalState[key] = 2
	return value, true
}
func (r *xsltRuntime) evaluate(i *xsltInstruction, e *xpathExpr, c xsltContext) xpathValue {
	f := r.frame
	oldNode, oldPosition, oldSize, oldBindings, oldEnvironment := f.node, f.position, f.size, f.bindings, f.environment
	f.node, f.position, f.size, f.bindings = c.node, c.position, c.size, i.namespaces
	f.environment = &xsltEnvironment{runtime: r, scope: c.scope, current: c.node, instruction: i}
	value := f.eval(e)
	f.node, f.position, f.size, f.bindings, f.environment = oldNode, oldPosition, oldSize, oldBindings, oldEnvironment
	return value
}
func (r *xsltRuntime) avt(i *xsltInstruction, name string, c xsltContext) string {
	var b strings.Builder
	for _, part := range i.avts["\x00"+name] {
		if part.expression != nil {
			value := r.evaluate(i, part.expression, c)
			r.frame.p.appendValue(&b, r.frame.string(value))
		} else {
			r.frame.p.appendValue(&b, part.text)
		}
	}
	return b.String()
}
func (r *xsltRuntime) instructionValue(i *xsltInstruction, c xsltContext) xpathValue {
	if e := i.expressions["select"]; e != nil {
		return r.evaluate(i, e, c)
	}
	if len(i.children) == 0 {
		return xpathStr("")
	}
	tree := &xsltResultTree{}
	c.target = xsltTarget{tree: tree}
	err := r.run([]xsltTask{{kind: xsltSequenceTask, sequence: i.children, context: c}})
	if err != nil {
		r.frame.p.err = err
	}
	return xpathValue{kind: xpathFragment, fragment: tree}
}
func (r *xsltRuntime) parameters(i *xsltInstruction, c xsltContext) map[string]xpathValue {
	values := make(map[string]xpathValue)
	for _, child := range i.children {
		if child.op != "with-param" {
			continue
		}
		key := child.name.key()
		value := r.instructionValue(child, c)
		if _, ok := values[key]; ok {
			r.fail()
			continue
		}
		values[key] = value
	}
	return values
}
func (r *xsltRuntime) selectTemplate(c xsltContext, imports bool) *xsltTemplate {
	f := r.frame
	var best *xsltTemplate
	priority := -1e300
	oldNode, oldBindings, oldEnvironment := f.node, f.bindings, f.environment
	f.node = c.node
	f.environment = &xsltEnvironment{runtime: r, scope: c.scope, current: c.node}
	defer func() { f.node, f.bindings, f.environment = oldNode, oldBindings, oldEnvironment }()
	for _, bucket := range r.templateCandidates(c) {
		for _, branch := range bucket {
			if !f.poll() {
				break
			}
			t, p := branch.template, branch.pattern
			if imports && (c.rule == nil || t.precedence >= c.rule.precedence) {
				continue
			}
			candidatePriority := p.priority
			if t.explicitPriority {
				candidatePriority = t.priority
			}
			if best != nil && (t.precedence < best.precedence || t.precedence == best.precedence && candidatePriority < priority) {
				continue
			}
			if p.matches(f, c.node) && (best == nil || t.precedence > best.precedence || candidatePriority > priority || t.order >= best.order) {
				best, priority = t, candidatePriority
			}
		}
	}
	return best
}
func (r *xsltRuntime) run(stack []xsltTask) error {
	r.runDepth++
	oldFocus := r.focus
	defer func() {
		r.runDepth--
		r.focus = oldFocus
	}()
	if r.runDepth > 3000 {
		r.frame.runtimeError("")
		return r.frame.p.err
	}
	f := r.frame
	push := func(t xsltTask) { stack = checkedAppend(&f.p, stack, t) }
	sequence := func(instructions []*xsltInstruction, c xsltContext) {
		c.scope = xsltNewScope(c.scope)
		push(xsltTask{kind: xsltSequenceTask, sequence: instructions, context: c})
	}
	for len(stack) > 0 && f.poll() {
		last := len(stack) - 1
		task := stack[last]
		stack = stack[:last]
		c, i := task.context, task.instruction
		switch task.kind {
		case xsltRestoreFocusTask:
			r.focus = xsltFocus{position: c.position, size: c.size}
			continue
		case xsltSequenceTask:
			if task.index >= len(task.sequence) {
				continue
			}
			i = task.sequence[task.index]
			task.index++
			push(task)
			push(xsltTask{kind: xsltInstructionTask, instruction: i, context: c, params: task.params})
			continue
		case xsltFinishBindingTask:
			c.scope.define(i.name.key(), xpathValue{kind: xpathFragment, fragment: task.fragment})
			continue
		case xsltFinishContentTask:
			value := xsltTreeString(&f.p, task.fragment)
			switch i.op {
			case "attribute":
				r.attribute(c.target, task.name, value)
			case "comment":
				if f.find(value, "--") >= 0 || strings.HasSuffix(value, "-") {
					r.fail()
					continue
				}
				r.appendResult(c.target, &xsltResultNode{Kind: xsltResultComment, Value: value})
			case "processing-instruction":
				if f.find(value, "?>") >= 0 {
					r.fail()
					continue
				}
				r.appendResult(c.target, &xsltResultNode{Kind: xsltResultPI, Name: task.name, Value: value})
			case "message":
				terminate, set := xsltAttr(i.source, "terminate")
				if terminate == "yes" {
					f.runtimeError("")
				} else if set && terminate != "no" {
					r.fail()
				}
			}
			continue
		case xsltApplyTask, xsltBuiltinTask:
			var t *xsltTemplate
			if task.kind == xsltBuiltinTask {
				var ok bool
				c, t, ok = r.builtinChild(&task)
				if !ok {
					continue
				}
				push(task)
			} else {
				imports := i != nil && i.op == "apply-imports"
				if imports && c.rule == nil {
					r.fail()
					continue
				}
				t = r.selectTemplate(c, imports)
				push(xsltTask{kind: xsltRestoreFocusTask, context: xsltContext{position: r.focus.position, size: r.focus.size}})
				r.focus = xsltFocus{position: c.position, size: c.size}
			}
			if c.depth >= 3000 {
				f.runtimeError("")
				continue
			}
			if t != nil {
				c.rule = t
				c.depth++
				c.scope = xsltNewScope(nil)
				push(xsltTask{kind: xsltSequenceTask, sequence: t.instruction.children, context: c, params: task.params})
			} else {
				switch c.node.kind {
				case xpathRoot, xpathElement:
					// A default traversal restores its caller's ambient XPath
					// focus after all children, including nested default rules.
					push(xsltTask{kind: xsltRestoreFocusTask, context: xsltContext{position: r.focus.position, size: r.focus.size}})
					push(xsltTask{kind: xsltBuiltinTask, context: c, params: task.params})
				case xpathText, xpathAttribute:
					r.appendResult(c.target, &xsltResultNode{Kind: xsltResultText, Value: f.stringValue(c.node)})
				}
			}
			continue
		}
		if i == nil {
			continue
		}
		switch i.op {
		case "ignored", "sort", "with-param", "fallback":
		case "text":
			value := i.source.value
			if i.source.kind == elementNode {
				var b strings.Builder
				for _, n := range i.source.children {
					if n.kind == textNode || n.kind == cdataNode {
						f.p.appendValue(&b, n.value)
					}
				}
				value = b.String()
			}
			r.appendResult(c.target, &xsltResultNode{Kind: xsltResultText, Value: value, DisableEscaping: xsltAttribute(i, "disable-output-escaping") == "yes"})
		case "value-of":
			value := r.evaluate(i, i.expressions["select"], c)
			r.appendResult(c.target, &xsltResultNode{Kind: xsltResultText, Value: f.string(value), DisableEscaping: xsltAttribute(i, "disable-output-escaping") == "yes"})
		case "copy-of":
			value := r.evaluate(i, i.expressions["select"], c)
			switch value.kind {
			case xpathNodes:
				r.copyNodes(c.target, value.nodes)
			case xpathFragment:
				r.copyFragment(c.target, value.fragment)
			default:
				r.appendResult(c.target, &xsltResultNode{Kind: xsltResultText, Value: f.string(value)})
			}
		case "variable", "param":
			key := i.name.key()
			if i.op == "param" {
				if value, exists := task.params[key]; exists {
					c.scope.defineParam(key, value)
					continue
				}
			}
			if _, exists := c.scope.lookup(key); exists && !(i.op == "variable" && c.scope.inheritedParam(key)) {
				r.fail()
				continue
			}
			if expr := i.expressions["select"]; expr != nil {
				c.scope.define(key, r.evaluate(i, expr, c))
			} else if len(i.children) == 0 {
				c.scope.define(key, xpathStr(""))
			} else {
				tree := &xsltResultTree{}
				push(xsltTask{kind: xsltFinishBindingTask, instruction: i, context: c, fragment: tree})
				next := c
				next.target = xsltTarget{tree: tree}
				sequence(i.children, next)
			}
		case "if", "when":
			if f.boolean(r.evaluate(i, i.expressions["test"], c)) {
				sequence(i.children, c)
			}
		case "choose":
			for _, child := range i.children {
				if child.op == "otherwise" || child.op == "when" && f.boolean(r.evaluate(child, child.expressions["test"], c)) {
					sequence(child.children, c)
					break
				}
			}
		case "apply-templates", "for-each":
			nodes := c.node.children
			if expr := i.expressions["select"]; expr != nil {
				value := r.evaluate(i, expr, c)
				if value.kind != xpathNodes {
					r.fail()
					continue
				}
				nodes = value.nodes
			}
			nodes = r.sortNodes(i, nodes, c)
			var params map[string]xpathValue
			if i.op == "apply-templates" {
				params = r.parameters(i, c)
			}
			for j := len(nodes) - 1; j >= 0; j-- {
				next := c
				next.node, next.position, next.size = nodes[j], j+1, len(nodes)
				if i.op == "apply-templates" {
					next.mode = i.mode
					push(xsltTask{kind: xsltApplyTask, context: next, params: params})
				} else {
					next.rule = nil
					sequence(i.children, next)
				}
			}
		case "call-template":
			t := r.program.named[i.name.key()]
			if t == nil {
				r.fail()
				continue
			}
			if c.depth >= 3000 {
				f.runtimeError("")
				continue
			}
			params := r.parameters(i, c)
			next := c
			next.depth++
			next.scope = xsltNewScope(nil)
			push(xsltTask{kind: xsltSequenceTask, sequence: t.instruction.children, context: next, params: params})
		case "apply-imports":
			push(xsltTask{kind: xsltApplyTask, instruction: i, context: c})
		case "literal", "element":
			name := r.alias(i.name)
			if i.op == "element" {
				var ok bool
				name, ok = r.dynamicName(i, c)
				if !ok {
					continue
				}
			}
			n := &xsltResultNode{Kind: xsltResultElement, Name: name}
			if i.op == "literal" {
				for _, ns := range i.namespaceOrder {
					prefix, uri := ns.Prefix, ns.URI
					if uri == xsltNamespaceURI || uri == xmlNamespaceURI || i.extensions[uri] {
						continue
					}
					local := false
					for _, declaration := range i.source.namespaces {
						if declaration.value == uri && (declaration.name == "xmlns" && prefix == "" || declaration.local == prefix) {
							local = true
							break
						}
					}
					if i.excluded[uri] && !local {
						continue
					}
					alias := r.alias(xsltName{Prefix: prefix, URI: uri})
					if uri != "" && alias.URI == "" {
						continue
					}
					r.namespace(n, alias.Prefix, alias.URI)
				}
			}
			n.Name = r.fixName(n, n.Name, false)
			r.appendResult(c.target, n)
			next := c
			next.target = xsltTarget{parent: n}
			r.useAttributeSets(i, next)
			if i.op == "literal" {
				for _, a := range i.source.attributes {
					if a.uri == xsltNamespaceURI {
						continue
					}
					var b strings.Builder
					for _, part := range i.avts[a.uri+"\x00"+a.local] {
						if part.expression != nil {
							f.p.appendValue(&b, f.string(r.evaluate(i, part.expression, c)))
						} else {
							f.p.appendValue(&b, part.text)
						}
					}
					name := xsltName{Prefix: a.prefix, Local: a.local, URI: a.uri}
					if name.URI != "" {
						name = r.alias(name)
					}
					r.attribute(next.target, name, b.String())
				}
			}
			sequence(i.children, next)
		case "copy":
			n := r.shallowCopy(c.target, c.node)
			next := c
			if c.node.kind == xpathElement {
				next.target = xsltTarget{parent: n}
				r.useAttributeSets(i, next)
			}
			if c.node.kind == xpathRoot || c.node.kind == xpathElement {
				sequence(i.children, next)
			}
		case "attribute", "comment", "processing-instruction", "message":
			if i.op == "attribute" {
				if c.target.parent == nil {
					continue
				}
				if len(c.target.parent.Children) > 0 {
					r.fail()
					continue
				}
			}
			var name xsltName
			if i.op == "attribute" || i.op == "processing-instruction" {
				var ok bool
				name, ok = r.dynamicName(i, c)
				if !ok {
					continue
				}
			}
			tree := &xsltResultTree{}
			push(xsltTask{kind: xsltFinishContentTask, instruction: i, context: c, fragment: tree, name: name})
			next := c
			next.target = xsltTarget{tree: tree}
			sequence(i.children, next)
		case "number":
			r.appendResult(c.target, &xsltResultNode{Kind: xsltResultText, Value: r.numberInstruction(i, c)})
		case "document":
			r.fail() // recognized secondary-output instruction, denied by transform security
		case "extension":
			found := false
			for j := len(i.children) - 1; j >= 0; j-- {
				child := i.children[j]
				if child.op == "fallback" {
					found = true
					sequence(child.children, c)
				}
			}
			if !found {
				r.fail()
			}
		default:
			// libxslt recovers an unknown XSLT instruction by executing its explicit
			// fallback children; absent fallback contributes no result nodes.
			for j := len(i.children) - 1; j >= 0; j-- {
				if i.children[j].op == "fallback" {
					sequence(i.children[j].children, c)
				}
			}
		}
	}
	return f.p.err
}
func (r *xsltRuntime) useAttributeSets(i *xsltInstruction, c xsltContext) {
	s := xsltAttribute(i, "use-attribute-sets")
	if i.op == "literal" {
		for _, a := range i.source.attributes {
			if a.uri == xsltNamespaceURI && a.local == "use-attribute-sets" {
				s = a.value
			}
		}
	}
	if s == "" {
		return
	}
	c.scope = xsltNewScope(nil)
	for _, lexical := range strings.Fields(s) {
		if !r.frame.poll() {
			return
		}
		name, ok := xsltQName(lexical, i.namespaces, false)
		if !ok {
			r.fail()
			return
		}
		// References have already been resolved into real attribute
		// instructions. An absent set contributes nothing, as in libxslt.
		_ = r.run([]xsltTask{{kind: xsltSequenceTask, sequence: r.program.attributeSets[name.key()], context: c}})
	}
}
