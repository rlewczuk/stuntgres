package xml

import "strings"

// Attribute sets are resolved after the ordinary compilation error gate. In
// libxslt 1.1.35 attributes.c, xsltResolveAttrSet marks a recursive target
// resolved, merges its existing attributes, and releases dependency edges.
// Its generic recursion report does not discard the successfully compiled style.
// Runtime application consumes only those resolved attributes, never the graph.
type xsltAttributeSet struct {
	attributes []xsltSetAttribute
	references []string
	state      uint8
}
type xsltSetAttribute struct {
	instruction *xsltInstruction
	local, uri  string
}

func xsltSetAttributeName(i *xsltInstruction) xsltSetAttribute {
	a := xsltSetAttribute{instruction: i}
	prefix := ""
	if !xsltAVTDynamic(i.avts["\x00name"]) {
		name := xsltAttribute(i, "name")
		var qualified bool
		prefix, a.local, qualified = strings.Cut(name, ":")
		if !qualified {
			a.local, prefix = prefix, ""
		}
	}
	if parts, explicit := i.avts["\x00namespace"]; explicit {
		if !xsltAVTDynamic(parts) {
			a.uri = xsltAttribute(i, "namespace")
		}
	} else if prefix != "" {
		a.uri = i.namespaces[prefix]
	}
	return a
}

func (c *xsltCompiler) resolveAttributeSets() error {
	sets := make(map[string]*xsltAttributeSet, len(c.program.attributeSets))
	for key, declarations := range c.program.attributeSets {
		set := &xsltAttributeSet{}
		sets[key] = set
		for _, declaration := range declarations {
			if c.p.err = c.p.ctx.Err(); c.p.err != nil {
				return c.p.err
			}
			for _, attribute := range declaration.children {
				if c.p.err = c.p.ctx.Err(); c.p.err != nil {
					return c.p.err
				}
				set.attributes = checkedAppend(&c.p, set.attributes, xsltSetAttributeName(attribute))
				if c.p.err != nil {
					return c.p.err
				}
			}
			for _, lexical := range strings.Fields(xsltAttribute(declaration, "use-attribute-sets")) {
				if c.p.err = c.p.ctx.Err(); c.p.err != nil {
					return c.p.err
				}
				name, ok := xsltQName(lexical, declaration.namespaces, false)
				if !ok {
					return xsltFailure(XSLTCompile, "")
				}
				key := name.key()
				duplicate := false
				for _, prior := range set.references {
					if c.p.err = c.p.ctx.Err(); c.p.err != nil {
						return c.p.err
					}
					if prior == key {
						duplicate = true
						break
					}
				}
				if !duplicate {
					set.references = checkedAppend(&c.p, set.references, key)
					if c.p.err != nil {
						return c.p.err
					}
				}
			}
		}
		if c.p.err != nil {
			return c.p.err
		}
	}
	type visit struct {
		set     *xsltAttributeSet
		index   int
		pending *xsltAttributeSet
	}
	// Declaration order gives a stable traversal without borrowing Go map order.
	for _, declaration := range c.program.root.children {
		if declaration.op != "attribute-set" {
			continue
		}
		set := sets[declaration.name.key()]
		if set.state == 2 {
			continue
		}
		set.state = 1
		stack := []visit{{set: set}}
		for len(stack) != 0 && c.p.err == nil {
			if c.p.err = c.p.ctx.Err(); c.p.err != nil {
				break
			}
			last := len(stack) - 1
			v := &stack[last]
			if v.pending != nil {
				// Merge inherited attributes only if their compiled name and
				// namespace are absent. Dynamic fields have no compiled name,
				// just like the released precomputed attribute representation.
				for _, inherited := range v.pending.attributes {
					duplicate := false
					for _, existing := range v.set.attributes {
						if c.p.err = c.p.ctx.Err(); c.p.err != nil {
							return c.p.err
						}
						if existing.local == inherited.local && existing.uri == inherited.uri {
							duplicate = true
							break
						}
					}
					if !duplicate {
						v.set.attributes = checkedAppend(&c.p, v.set.attributes, inherited)
						if c.p.err != nil {
							return c.p.err
						}
					}
				}
				v.pending = nil
			}
			if v.index == len(v.set.references) {
				v.set.references = nil
				v.set.state = 2
				stack = stack[:last]
				continue
			}
			reference := sets[v.set.references[v.index]]
			v.index++
			if reference == nil {
				continue
			}
			v.pending = reference
			if reference.state == 1 {
				reference.state = 2
			} else if reference.state == 0 && len(stack) < 100 {
				reference.state = 1
				stack = checkedAppend(&c.p, stack, visit{set: reference})
			}
		}
		if c.p.err != nil {
			return c.p.err
		}
	}
	for key, set := range sets {
		var attributes []*xsltInstruction
		for _, attribute := range set.attributes {
			if c.p.err = c.p.ctx.Err(); c.p.err != nil {
				return c.p.err
			}
			attributes = checkedAppend(&c.p, attributes, attribute.instruction)
			if c.p.err != nil {
				return c.p.err
			}
		}
		c.program.attributeSets[key] = attributes
	}
	return c.p.err
}
