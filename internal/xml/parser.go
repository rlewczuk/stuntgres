package xml

func (p *parser) top() {
	if p.stopped || p.err != nil {
		return
	}
	if p.has("\xff\xfe") || p.has("\xfe\xff") {
		p.event("XML_ERR_INTERNAL_ERROR", "internal error: detected an error in element content")
		return
	}
	if p.effective == Document {
		if len(p.s) == 0 {
			if !p.policy.documentContext {
				p.event("XML_ERR_INTERNAL_ERROR", "switching encoding : no input")
			}
			p.event("XML_ERR_DOCUMENT_EMPTY", "Document is empty")
			return
		}
		if p.policy.documentContext && p.i == len(p.s) {
			p.event("XML_ERR_DOCUMENT_EMPTY", "Document is empty")
			return
		}
		p.misc()
		if p.err != nil || p.stopped {
			return
		}
		if p.has("<!DOCTYPE") {
			p.dtd()
			p.misc()
		}
		if p.err != nil || p.stopped {
			return
		}
		if p.i == len(p.s) {
			p.event("XML_ERR_DOCUMENT_EMPTY", "Start tag expected, '<' not found")
			return
		}
		if !p.has("<") || p.has("</") || p.has("<!") && p.dtdState == nil {
			p.event("XML_ERR_DOCUMENT_EMPTY", "Start tag expected, '<' not found")
			return
		}
		p.element()
		p.rootSeen = true
		if p.stopped || p.err != nil {
			return
		}
		p.misc()
		if p.i < len(p.s) && p.err == nil {
			p.event("XML_ERR_DOCUMENT_END", "Extra content at the end of the document")
		}
		return
	}
	for p.i < len(p.s) && p.poll() && !p.stopped {
		// Content's RAW==0 boundary ends continuation of an already-failed parse.
		// Do not reinterpret that terminator as CharData after a production returns;
		// clean input still requires complete-byte validation, not prefix acceptance.
		if p.byte() == 0 && p.illFormed {
			return
		}
		before := p.i
		switch {
		case p.has("<!DOCTYPE") && p.illFormed:
			p.stopped = true
			return
		case p.has("<!--"):
			p.comment()
		case p.has("<?"):
			p.pi()
		case p.has("<![CDATA["):
			p.cdata()
			p.rootSeen = true
		case p.has("</"):
			p.event("XML_ERR_NOT_WELL_BALANCED", "chunk is not well balanced")
			p.stopped = true
		case p.has("<"):
			p.element()
			p.rootSeen = true
		default:
			p.text()
		}
		if !p.contentBoundary() {
			return
		}
		if p.i == before {
			return
		}
	}
}
func (p *parser) misc() {
	for p.i < len(p.s) && p.poll() && !p.stopped {
		p.spaces()
		if p.has("<!--") {
			p.comment()
		} else if p.has("<?") {
			p.pi()
		} else {
			return
		}
	}
}
func (p *parser) text() {
	if p.cdataStale {
		if p.byte() == '&' {
			p.contentReference()
		} else {
			p.failedCDataText()
		}
		return
	}
	start := p.i
	ascii := true
	collectStart := p.i
	collectComplex := false
	callbackBytes := 0
	callbacks := p.tree != nil && p.ignoreBlanks
	flush := func() {
		if p.i > collectStart {
			lookahead := p.i
			if collectComplex && lookahead+1 < len(p.s) && p.s[lookahead] == '\r' && p.s[lookahead+1] == '\n' {
				lookahead++
			}
			p.collectCharacters(p.s[collectStart:p.i], collectComplex, lookahead)
		}
		collectStart = p.i
		callbackBytes = 0
	}
	defer flush()
	for p.i < len(p.s) && p.byte() != '<' && p.poll() {
		if p.depth == 0 && !space(p.byte()) {
			p.rootSeen = true
		}
		if p.has("]]>") {
			at := p.i
			if ascii {
				p.i = start
			}
			p.event("XML_ERR_MISPLACED_CDATA_END", "Sequence ']]>' not allowed in content")
			if ascii {
				p.i = at + 1
			} else {
				p.advance(1)
			}
			start = p.i
			ascii = true
			continue
		}
		if p.byte() == '&' {
			if p.i != start && !p.contentBoundary() {
				return
			}
			flush()
			p.contentReference()
			collectStart = p.i
			collectComplex = false
			callbackBytes = 0
			if !p.contentBoundary() {
				return
			}
			start = p.i
			ascii = true
			continue
		}
		if p.byte() == 0 && p.depth > 0 {
			return
		}
		at := p.i
		crlf := p.byte() == '\r' && len(p.s)-p.i > 1 && p.s[p.i+1] == '\n'
		if callbacks && !collectComplex && (p.byte() >= 128 || p.byte() == '\r') {
			// ASCII callbacks end before CR/non-ASCII. CRLF resumes the accelerator;
			// a lone CR or non-ASCII scalar enters the complex 300-byte buffer.
			flush()
			if !crlf {
				collectComplex = true
			}
		}
		if p.byte() >= 128 || p.byte() == '\r' && !crlf {
			ascii = false
		}
		width := p.character()
		if width == 0 {
			if p.err == nil {
				p.event("XML_ERR_INTERNAL_ERROR", "internal error: detected an error in element content")
			}
			p.stopped = true
			return
		}
		if callbacks && collectComplex {
			callbackBytes += width
			if callbackBytes >= 300 {
				flush()
			}
		}
		// The ASCII accelerator commits the LF cursor before its CRLF restart.
		if ascii && crlf {
			start = at + 1
		}
	}
}
