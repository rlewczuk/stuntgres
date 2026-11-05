package xml

import (
	"context"
	"errors"
	"testing"
)

func TestXSLTQNames(t *testing.T) {
	t.Run("lexical_and_binding", func(t *testing.T) {
		bindings := map[string]string{"": "urn:default", "p": "urn:p", "alias": "urn:p"}
		for _, lexical := range []string{"", ":", ":out", "::out", "p:", "p:out:more", "p::out", "1out", "1p:out", "p:1out", "out name", " out", "out ", "{urn:p}out"} {
			if prefix, local, ok := xsltLexicalQName(lexical); ok {
				t.Fatalf("malformed lexical QName %q accepted as %q:%q", lexical, prefix, local)
			}
			for _, defaultNS := range []bool{false, true} {
				if name, ok := xsltQName(lexical, bindings, defaultNS); ok || name != (xsltName{}) {
					t.Fatalf("malformed QName %q bound as %+v, accepted=%v", lexical, name, ok)
				}
			}
		}
		for _, tc := range []struct {
			lexical   string
			defaultNS bool
			want      xsltName
		}{
			{"out", false, xsltName{Local: "out"}},
			{"out", true, xsltName{Local: "out", URI: "urn:default"}},
			{"p:out", true, xsltName{Prefix: "p", Local: "out", URI: "urn:p"}},
			{"alias:out", false, xsltName{Prefix: "alias", Local: "out", URI: "urn:p"}},
			{"xml:lang", false, xsltName{Prefix: "xml", Local: "lang", URI: xmlNamespaceURI}},
			{"_out-1.2", false, xsltName{Local: "_out-1.2"}},
			{"é:名", false, xsltName{Prefix: "é", Local: "名", URI: "urn:unicode"}},
		} {
			bindings["é"] = "urn:unicode"
			prefix, local, valid := xsltLexicalQName(tc.lexical)
			if !valid || prefix != tc.want.Prefix || local != tc.want.Local {
				t.Fatalf("lexical QName %q = %q:%q, valid=%v", tc.lexical, prefix, local, valid)
			}
			if name, ok := xsltQName(tc.lexical, bindings, tc.defaultNS); !ok || name != tc.want {
				t.Fatalf("QName %q = %+v, valid=%v; want %+v", tc.lexical, name, ok, tc.want)
			}
		}
		if _, ok := xsltQName("missing:out", bindings, false); ok {
			t.Fatal("unbound QName accepted")
		}
		unqualified, _ := xsltQName("out", bindings, false)
		bound, _ := xsltQName("p:out", bindings, false)
		alias, _ := xsltQName("alias:out", bindings, false)
		if unqualified.key() == bound.key() || bound.key() != alias.key() {
			t.Fatal("expanded-name binding lost namespace identity")
		}
	})

	t.Run("compile_stages", func(t *testing.T) {
		for _, tc := range []struct{ name, declarations, body string }{
			{"element", ``, `<xsl:element name=":out"/>`},
			{"attribute", ``, `<e><xsl:attribute name=":a">V</xsl:attribute></e>`},
			{"element_explicit_namespace", ``, `<xsl:element name=":out" namespace="urn:p"/>`},
			{"attribute_explicit_namespace", ``, `<e><xsl:attribute name=":a" namespace="urn:p"/></e>`},
			{"template_declaration", `<xsl:template name=":n">N</xsl:template>`, `T`},
			{"template_reference", `<xsl:template name="n">N</xsl:template>`, `<xsl:call-template name=":n"/>`},
			{"variable", `<xsl:variable name=":v" select="'V'"/>`, `T`},
			{"parameter", `<xsl:param name=":p" select="'D'"/>`, `T`},
			{"local_variable", ``, `<xsl:variable name=":v" select="'V'"/>`},
			{"local_parameter", ``, `<xsl:param name=":p" select="'D'"/>`},
			{"with_parameter", `<xsl:template name="n"><xsl:param name="p"/></xsl:template>`, `<xsl:call-template name="n"><xsl:with-param name=":p" select="'X'"/></xsl:call-template>`},
			{"mode_declaration", `<xsl:template match="n" mode=":m">N</xsl:template>`, `T`},
			{"mode_reference", `<xsl:template match="n" mode="m">N</xsl:template>`, `<xsl:apply-templates select="r/n" mode=":m"/>`},
			{"attribute_set", `<xsl:attribute-set name=":s"/>`, `T`},
			{"key", `<xsl:key name=":k" match="n" use="@id"/>`, `T`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				xsltExpectError(t, `<r><n/></r>`, xsltTestStylesheet(tc.declarations, tc.body), "", XSLTCompile, "")
			})
		}
		program := xsltTestProgram(t, xsltTestStylesheet(`<xsl:template name="n">N</xsl:template><xsl:template xmlns:p="urn:p" name="p:n" mode="p:m" match="n">P</xsl:template><xsl:variable name="v" select="'V'"/><xsl:param xmlns:p="urn:p" name="p:v" select="'P'"/>`, `<xsl:call-template name="n"/>`))
		plain := program.named["\x00n"]
		bound := program.named["urn:p\x00n"]
		if plain == nil || bound == nil || plain == bound || bound.mode != "urn:p\x00m" || program.globals["\x00v"] == nil || program.globals["urn:p\x00v"] == nil || len(program.named) != 2 || len(program.globals) != 2 {
			t.Fatal("compiled declarations lost separate expanded-name identities")
		}
	})

	t.Run("dynamic_namespaces", func(t *testing.T) {
		for _, tc := range []struct {
			name, body string
			want       xsltName
		}{
			{"unqualified", `<xsl:element name="{'out'}"/>`, xsltName{Local: "out"}},
			{"default_element", `<xsl:element xmlns="urn:default" name="{'out'}"/>`, xsltName{Local: "out", URI: "urn:default"}},
			{"default_attribute", `<xsl:attribute xmlns="urn:default" name="{'a'}"/>`, xsltName{Local: "a"}},
			{"bound_element", `<xsl:element xmlns:p="urn:p" name="{'p:out'}"/>`, xsltName{Prefix: "p", Local: "out", URI: "urn:p"}},
			{"bound_attribute", `<xsl:attribute xmlns:p="urn:p" name="{'p:a'}"/>`, xsltName{Prefix: "p", Local: "a", URI: "urn:p"}},
			{"explicit_binding", `<xsl:element name="{'q:out'}" namespace="urn:q"/>`, xsltName{Prefix: "q", Local: "out", URI: "urn:q"}},
			{"static_explicit_binding", `<xsl:element name="q:out" namespace="urn:q"/>`, xsltName{Prefix: "q", Local: "out", URI: "urn:q"}},
			{"override_binding", `<xsl:element xmlns:p="urn:old" name="{'p:out'}" namespace="urn:new"/>`, xsltName{Prefix: "p", Local: "out", URI: "urn:new"}},
			{"empty_namespace", `<xsl:element name="{'q:out'}" namespace=""/>`, xsltName{Local: "out"}},
			{"xml_binding", `<xsl:attribute name="{'xml:lang'}"/>`, xsltName{Prefix: "xml", Local: "lang", URI: xmlNamespaceURI}},
			{"xml_namespace_fixup", `<xsl:attribute name="{'q:lang'}" namespace="http://www.w3.org/XML/1998/namespace"/>`, xsltName{Prefix: "xml", Local: "lang", URI: xmlNamespaceURI}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := xsltQNameTestRuntime(t, xsltTestStylesheet("", tc.body))
				i := r.program.templates[0].instruction.children[0]
				oldP, hadP := i.namespaces["p"]
				oldQ, hadQ := i.namespaces["q"]
				name, ok := r.dynamicName(i, xsltContext{node: r.source.root, position: 1, size: 1, scope: xsltNewScope(nil)})
				if !ok || r.failed || r.frame.p.err != nil || name != tc.want {
					t.Fatalf("dynamic name=%+v valid=%v failed=%v error=%v; want %+v", name, ok, r.failed, r.frame.p.err, tc.want)
				}
				newP, hasP := i.namespaces["p"]
				newQ, hasQ := i.namespaces["q"]
				if oldP != newP || hadP != hasP || oldQ != newQ || hadQ != hasQ {
					t.Fatal("dynamic namespace override mutated compiled bindings")
				}
			})
		}
		for _, op := range []string{"element", "attribute"} {
			r := xsltQNameTestRuntime(t, xsltTestStylesheet("", `<xsl:`+op+` name="{':out'}"/>`))
			i := r.program.templates[0].instruction.children[0]
			name, _ := r.dynamicName(i, xsltContext{node: r.source.root, position: 1, size: 1, scope: xsltNewScope(nil)})
			if !r.failed || r.frame.p.err != nil || name.Prefix != "" || name.Local != ":out" || name.key() == "\x00out" {
				t.Fatalf("malformed dynamic %s name=%+v failed=%v error=%v", op, name, r.failed, r.frame.p.err)
			}
		}
		for _, body := range []string{`<xsl:element name="{':out'}"/>`, `<xsl:element name="{':out'}" namespace="urn:p"/>`, `<e><xsl:attribute name="{':a'}">V</xsl:attribute></e>`} {
			xsltExpectError(t, `<r/>`, xsltTestStylesheet("", body), "", XSLTApply, "")
		}
		// Generic dynamic errors retain later structured callbacks rather than
		// converting transform ERROR into STOPPED or publishing a partial tree.
		xsltExpectError(t, `<r/>`, xsltTestStylesheet("", `<xsl:element name="{':out'}"/><xsl:value-of select="$missing"/>`), "", XSLTApply, "Undefined variable")
	})

	t.Run("sql_parameter_names_remain_separate", func(t *testing.T) {
		style := `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p"><xsl:output method="text"/><xsl:param name="p" select="'D'"/><xsl:param name="p:q" select="'Q'"/><xsl:template match="/"><xsl:value-of select="$p"/>|<xsl:value-of select="$p:q"/></xsl:template></xsl:stylesheet>`
		r := xsltQNameTestRuntime(t, style)
		pairs, err := xsltSplitParams(context.Background(), ":p='X'")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.userParameters(pairs); err != nil || r.failed {
			t.Fatalf("leading-colon SQL parameter failed: %v, transform failed=%v", err, r.failed)
		}
		value, exists := r.user["\x00:p"]
		if !exists || value.kind != xpathString || value.text != "X" {
			t.Fatal("released user-parameter splitter did not retain distinct lexical local name", r.user)
		}
		if _, aliases := r.user["\x00p"]; aliases {
			t.Fatal("leading-colon SQL name aliases declared p")
		}
		for _, tc := range []struct{ params, want string }{
			{":p='X'", "D|Q"},
			{":p='X',p='P'", "P|Q"},
			{"p:q='X'", "D|X"},
			{"{urn:p}q='X'", "D|X"},
			{"q='X'", "D|Q"},
		} {
			xsltExpectText(t, `<r/>`, style, tc.params, tc.want)
		}
		xsltExpectError(t, `<r/>`, style, ":p=[", XSLTApply, "Invalid expression")
		xsltExpectError(t, `<r/>`, style, ":p='X',:p='Y'", XSLTApply, "")
		xsltExpectError(t, `<r/>`, style, "missing:q='X'", XSLTApply, "")
	})
}

func xsltQNameTestRuntime(t *testing.T, style string) *xsltRuntime {
	t.Helper()
	program := xsltTestProgram(t, style)
	_, doc, _, err := parseConfigured(context.Background(), `<r/>`, Options{Mode: Document, InputKind: RawMemory}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, legacyDiagnostics: true, xsltReader: true})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := newXPathTree(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	return &xsltRuntime{program: program, frame: frame, source: frame.tree, user: make(map[string]xpathValue)}
}

func TestXSLTQNameCancellation(t *testing.T) {
	for _, tc := range []struct{ phase, body, params string }{
		{"instruction", `<xsl:element name=":out"/>`, ""},
		{"avt", `<xsl:element name="{':out'}"/>`, ""},
		{"userParameters", `<xsl:value-of select="$p"/>`, ":p='X'"},
	} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			ctx := &phaseContext{Context: context.Background(), phase: tc.phase, after: 2, cause: cause}
			result, err := ProcessXSLT(ctx, `<r/>`, xsltTestStylesheet(`<xsl:param name="p" select="'D'"/>`, tc.body), tc.params)
			var failure *XSLTError
			if !ctx.admitted || !errors.Is(err, cause) || errors.As(err, &failure) || result != (XSLTResult{}) {
				t.Fatalf("phase=%s admitted=%v result=%+v error=%v; want ordinary %v", tc.phase, ctx.admitted, result, err, cause)
			}
			xsltExpectText(t, `<r/>`, xsltTestStylesheet(`<xsl:param name="p" select="'D'"/>`, `<xsl:value-of select="$p"/>`), ":p='X'", "D")
		}
	}
}
