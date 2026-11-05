package xml

import (
	"context"
	"testing"
)

func TestXSLTStylesheetDocument(t *testing.T) {
	style := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:d="urn:d" version="1.0"><d:data><!--data--></d:data><xsl:output method="text"/><xsl:template match="/"><!--instruction--><xsl:text>T</xsl:text><xsl:value-of select="count(document('')//xsl:text)"/>|<xsl:value-of select="count(document('')//comment())"/>|<xsl:value-of select="name(document('')/*)"/></xsl:template></xsl:stylesheet>`
	xsltExpectText(t, "<r/>", style, "", "T0|1|xsl:stylesheet")
	p := xsltTestProgram(t, style)
	if p.style.root.children[0].source != p.root.source {
		t.Fatal("stylesheet projection replaced the parsed root")
	}
	root := p.style.root.children[0]
	var data, template *xpathNode
	for _, n := range root.children {
		if n.local == "data" && n.uri == "urn:d" {
			data = n
		}
		if n.local == "template" && n.uri == xsltNamespaceURI {
			template = n
		}
	}
	if data == nil || template == nil || len(data.children) != 1 || data.children[0].kind != xpathComment {
		t.Fatal("foreign data was preprocessed")
	}
	for _, n := range template.children {
		if n.kind == xpathComment || n.kind == xpathElement && n.local == "text" && n.uri == xsltNamespaceURI {
			t.Fatal("stylesheet-only wrapper survived compilation")
		}
	}
	// Compiled instructions still own the detached language wrapper and its real
	// text; rewriting document visibility must not mutate executable semantics.
	if p.templates[0].instruction.children[0].source.kind != elementNode || p.templates[0].instruction.children[0].op != "text" {
		t.Fatal("compiled text instruction lost")
	}
	if err := p.preprocessStyleTree(context.Background()); err != nil {
		t.Fatal(err)
	}
}
