package xml

func (r *xsltRuntime) numberInstruction(i *xsltInstruction, c xsltContext) string {
	f := r.frame
	var values []float64
	if expr := i.expressions["value"]; expr != nil {
		values = []float64{f.number(r.evaluate(i, expr, c))}
	} else {
		count, from := i.numberCount, i.numberFrom
		oldNode, oldBindings, oldEnvironment := f.node, f.bindings, f.environment
		f.node = c.node
		f.bindings = i.namespaces
		f.environment = &xsltEnvironment{runtime: r, scope: c.scope, current: c.node, instruction: i}
		defer func() { f.node, f.bindings, f.environment = oldNode, oldBindings, oldEnvironment }()
		matches := func(patterns []xsltPattern, n *xpathNode) bool {
			for at := range patterns {
				if patterns[at].matches(f, n) {
					return true
				}
			}
			return false
		}
		counts := func(n *xpathNode) bool {
			if len(count) > 0 {
				return matches(count, n)
			}
			return n.kind == c.node.kind && (n.kind != xpathElement && n.kind != xpathAttribute || n.local == c.node.local && n.uri == c.node.uri) && (n.kind != xpathPI || n.name == c.node.name)
		}
		level := xsltAttribute(i, "level")
		if level == "" {
			level = "single"
		}
		switch level {
		case "any":
			total := 0.0
			// Reverse document traversal excludes attributes/namespaces except the real
			// initial node, then visits preceding nodes and ancestors exactly once.
			n := c.node
			for n != nil && f.poll() {
				if counts(n) {
					total++
				}
				if len(from) > 0 && matches(from, n) {
					break
				}
				if n.kind == xpathRoot {
					break
				}
				if n.kind == xpathAttribute || n.kind == xpathNamespace {
					n = n.parent
					continue
				}
				owner, at := n.owner, n.order-1
				n = nil
				for at >= 0 && f.poll() {
					candidate := owner.ordered[at]
					at--
					if candidate.kind != xpathAttribute && candidate.kind != xpathNamespace {
						n = candidate
						break
					}
				}
			}
			values = []float64{total}
		case "single", "multiple":
			for n := c.node; n != nil && n.kind != xpathRoot && f.poll(); n = n.parent {
				if len(from) > 0 && matches(from, n) {
					break
				}
				if !counts(n) {
					continue
				}
				total := 1.0
				if n.parent != nil && n.kind != xpathAttribute && n.kind != xpathNamespace {
					for _, sibling := range n.parent.children {
						if !f.poll() || sibling == n {
							break
						}
						if counts(sibling) {
							total++
						}
					}
				}
				values = checkedAppend(&f.p, values, total)
				if level == "single" {
					break
				}
			}
			for lo, hi := 0, len(values)-1; lo < hi; lo, hi = lo+1, hi-1 {
				values[lo], values[hi] = values[hi], values[lo]
			}
		}
	}
	format := r.avt(i, "format", c)
	if _, set := i.avts["\x00format"]; !set {
		format = "1"
	}
	result, err := formatXSLTNumber(f.p.ctx, values, format, r.avt(i, "grouping-separator", c), r.avt(i, "grouping-size", c), r.avt(i, "letter-value", c), r.avt(i, "lang", c))
	if err != nil {
		f.p.err = err
	}
	return result
}
