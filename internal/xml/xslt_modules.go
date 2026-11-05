package xml

import (
	"errors"
	"fmt"
	"net/url"
)

// The PostgreSQL entity loader supplies an actual zero-length input stream for
// external XML. Compilation parses that stream; no file, network or fabricated
// stylesheet tree is ever substituted. Transform security is a separate policy.
func (c *xsltCompiler) externalModule(i *xsltInstruction) {
	href, ok := xsltAttr(i.source, "href")
	if !ok || href == "" {
		c.fail("")
		return
	}
	resolved := xsltResolveBase(i.baseURI, href)
	if _, err := url.Parse(resolved); err != nil {
		c.fail("")
		return
	}
	_, _, events, err := parseConfigured(c.p.ctx, "", Options{Mode: Document, InputKind: Text}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, documentContext: true, legacyDiagnostics: true})
	var parseError *ParseError
	if errors.As(err, &parseError) {
		detail, renderError := xsltDiagnosticText(c.p.ctx, events)
		if renderError != nil {
			c.p.err = renderError
		} else {
			c.fail(detail)
		}
		return
	}
	if err != nil {
		c.p.err = err
		return
	}
	c.p.err = fmt.Errorf("empty XSLT module input unexpectedly produced an accepted document")
}
