package xml

// validateXSLTChildren checks placement, not demand: malformed known language
// constructs are compile errors even inside a branch the transform won't visit.
func (c *xsltCompiler) validateXSLTChildren(i *xsltInstruction) {
	if c.p.err != nil {
		return
	}
	if i.op == "stylesheet" || i.op == "transform" {
		body := false
		for _, child := range i.children {
			if child.op == "ignored" {
				continue
			}
			if child.op == "import" {
				if body {
					c.fail("")
				}
			} else {
				body = true
			}
		}
		return
	}
	required := []string(nil)
	switch i.op {
	case "element", "attribute":
		required = []string{"name"}
	case "strip-space", "preserve-space":
		required = []string{"elements"}
	case "namespace-alias":
		required = []string{"stylesheet-prefix", "result-prefix"}
	}
	for _, name := range required {
		if _, ok := xsltAttr(i.source, name); !ok {
			c.fail("")
			return
		}
	}
	if (i.op == "element" || i.op == "attribute") && !xsltAVTDynamic(i.avts["\x00name"]) {
		name := xsltAttribute(i, "name")
		prefix, _, valid := xsltLexicalQName(name)
		if !valid || i.op == "attribute" && name == "xmlns" {
			c.fail("")
			return
		}
		if prefix != "" {
			if _, hasNamespace := xsltAttr(i.source, "namespace"); !hasNamespace {
				if _, bound := i.namespaces[prefix]; !bound {
					c.fail("")
					return
				}
			}
		}
	}
	seenBody, seenOtherwise, seenWhen := false, false, false
	for _, child := range i.children {
		if c.p.err = c.p.ctx.Err(); c.p.err != nil {
			return
		}
		if child.op == "ignored" {
			continue
		}
		switch i.op {
		case "choose":
			if child.op == "when" && !seenOtherwise {
				seenWhen = true
			} else if child.op == "otherwise" && seenWhen && !seenOtherwise {
				seenOtherwise = true
			} else {
				c.fail("")
			}
		case "apply-templates":
			if child.op == "sort" && !seenBody {
			} else if child.op == "with-param" {
				seenBody = true
			} else {
				c.fail("")
			}
		case "call-template":
			if child.op != "with-param" {
				c.fail("")
			}
		case "attribute-set":
			if child.op != "attribute" {
				c.fail("")
			}
		case "apply-imports", "value-of", "copy-of", "number", "sort", "include", "import", "output", "key", "strip-space", "preserve-space", "namespace-alias", "decimal-format":
			c.fail("")
		default:
			if child.source.uri == xsltNamespaceURI {
				switch child.op {
				case "sort":
					if i.op != "for-each" || seenBody {
						c.fail("")
					}
				case "param":
					if i.op != "template" || seenBody {
						c.fail("")
					}
				case "when", "otherwise", "with-param":
					c.fail("")
				case "stylesheet", "transform", "template", "output", "key", "decimal-format", "attribute-set", "namespace-alias", "strip-space", "preserve-space", "include", "import":
					if i.op != "stylesheet" && i.op != "transform" {
						c.fail("")
					}
				}
			}
			if child.op != "sort" && child.op != "param" {
				seenBody = true
			}
		}
	}
	if i.op == "choose" && !seenWhen {
		c.fail("")
	}
	if i.op == "number" {
		if level, ok := xsltAttr(i.source, "level"); ok && level != "single" && level != "multiple" && level != "any" {
			c.fail("")
		}
	}
}
