package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func xsltTestStylesheet(top, body string) string {
	return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0">` + top + `<xsl:output method="text"/><xsl:template match="/">` + body + `</xsl:template></xsl:stylesheet>`
}
func xsltExpectText(t *testing.T, source, style, params, want string) {
	t.Helper()
	got, err := ProcessXSLT(context.Background(), source, style, params)
	if err != nil || got.Null || got.Text != want {
		t.Fatalf("result %q null=%v error=%v; want %q", got.Text, got.Null, err, want)
	}
}
func xsltExpectError(t *testing.T, source, style, params string, stage XSLTStage, detail string) {
	t.Helper()
	got, err := ProcessXSLT(context.Background(), source, style, params)
	var failure *XSLTError
	if !errors.As(err, &failure) || failure.Stage != stage || failure.Detail != detail || got != (XSLTResult{}) {
		t.Fatalf("result %+v error %#v; want stage %v detail %q", got, err, stage, detail)
	}
}
func xsltTestProgram(t *testing.T, style string) *xsltProgram {
	t.Helper()
	_, doc, _, err := parseConfigured(context.Background(), style, Options{Mode: Document, InputKind: RawMemory}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, legacyDiagnostics: true, xsltReader: true})
	if err != nil {
		t.Fatal(err)
	}
	program, err := compileXSLT(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func TestXSLTCompile(t *testing.T) {
	style := `<s:stylesheet xmlns:s="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p" version="1.0" xml:base="https://example.invalid/a/">
<s:param name="p:v" select="'G'"/><s:output method="text"/><s:template match="/" xml:base="b/"><s:value-of select="$p:v"/></s:template></s:stylesheet>`
	p := xsltTestProgram(t, style)
	if p.globals["urn:p\x00v"] == nil || p.globals["urn:p\x00v"].expressions["select"] == nil || len(p.templates) != 1 || len(p.dispatch) == 0 {
		t.Fatal("missing real declarations or dispatch index")
	}
	instruction := p.templates[0].instruction
	if instruction.line != 2 || instruction.baseURI != "https://example.invalid/a/b/" || instruction.namespaces["p"] != "urn:p" {
		t.Fatal(instruction.line, instruction.baseURI, instruction.namespaces)
	}
	xsltExpectText(t, "<r/>", style, "", "G")
	xsltExpectText(t, "<r/>", `<out xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xsl:version="1.0">T</out>`, "", "<?xml version=\"1.0\"?>\n<out>T</out>\n")
	for _, style := range []string{`<bad/>`, xsltTestStylesheet("", `<xsl:value-of select="["/>`), xsltTestStylesheet("", `<xsl:choose><xsl:otherwise>X</xsl:otherwise></xsl:choose>`), xsltTestStylesheet("", `<xsl:element name="bad name"/>`)} {
		_, err := ProcessXSLT(context.Background(), "<r/>", style, "")
		var failure *XSLTError
		if !errors.As(err, &failure) || failure.Stage != XSLTCompile {
			t.Fatalf("compile envelope %v", err)
		}
	}
}
func TestXSLTPatterns(t *testing.T) {
	xsltExpectText(t, `<r><n/><n x="1"/></r>`, xsltTestStylesheet(`<xsl:template match="n">N</xsl:template><xsl:template match="n[@x]">P</xsl:template><xsl:template match="*">W<xsl:apply-templates/></xsl:template>`, `<xsl:apply-templates select="/r/n"/>`), "", "NP")
	xsltExpectText(t, `<r><n/><n x="1"/></r>`, xsltTestStylesheet(`<xsl:template match="n" priority="2">N</xsl:template><xsl:template match="n[@x]">P</xsl:template>`, `<xsl:apply-templates select="/r/n"/>`), "", "NN")
	xsltExpectText(t, `<r><group><n>A</n><n>B</n></group><n>C</n></r>`, xsltTestStylesheet(`<xsl:template match="r//n[1]">[<xsl:value-of select="."/>]</xsl:template>`, `<xsl:apply-templates select="//n"/>`), "", "[A]B[C]")
	for _, pattern := range []string{"1+2", "self::n", "n[$v]", "n[current()]", "ancestor::n"} {
		if _, err := compileXSLTPattern(context.Background(), pattern, nil, false); err == nil {
			t.Fatalf("accepted non-pattern %q", pattern)
		}
	}
	_, d, err := parseExpanded(context.Background(), `<r><n/><n/></r>`, Options{Mode: Document})
	if err != nil {
		t.Fatal(err)
	}
	f, err := newXPathTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	patterns, err := compileXSLTPattern(context.Background(), "n[2]|/", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	children := f.tree.root.children[0].children
	if patterns[0].matches(f, children[0]) || !patterns[0].matches(f, children[1]) || !patterns[1].matches(f, f.tree.root) {
		t.Fatal("sibling predicate/root pattern mismatch")
	}
}
func TestXSLTTemplates(t *testing.T) {
	top := `<xsl:template match="n">[<xsl:value-of select="."/>]</xsl:template>`
	xsltExpectText(t, `<r><n>A</n><n>B</n></r>`, xsltTestStylesheet(top, `<xsl:apply-templates select="/r/n"/>`), "", "[A][B]")
	xsltExpectText(t, `<r><n>A</n><n>B</n></r>`, xsltTestStylesheet(top, `<xsl:apply-templates select="/r/n[2]"/><xsl:apply-templates select="/r/n[1]"/>`), "", "[B][A]")
	xsltExpectText(t, `<r>A<n>B</n><!--C--><?p D?></r>`, xsltTestStylesheet("", `<xsl:apply-templates select="/r"/>`), "", "AB")
	xsltExpectText(t, `<r><n>A</n><n>B</n></r>`, xsltTestStylesheet(`<xsl:template match="n" mode="m">M<xsl:value-of select="."/></xsl:template><xsl:template match="n">D</xsl:template>`, `<xsl:apply-templates select="/r/n" mode="m"/>`), "", "MAMB")
	xsltExpectText(t, `<r/>`, xsltTestStylesheet(`<xsl:variable name="v" select="'G'"/><xsl:template name="named"><xsl:param name="v"/><xsl:value-of select="$v"/></xsl:template>`, `<xsl:call-template name="named"><xsl:with-param name="v" select="'X'"/></xsl:call-template><xsl:value-of select="$v"/>`), "", "XG")
	xsltExpectText(t, `<r/>`, xsltTestStylesheet("", `<xsl:if test="false()"><xsl:value-of select="$missing"/></xsl:if><xsl:choose><xsl:when test="false()">F</xsl:when><xsl:otherwise>T</xsl:otherwise></xsl:choose>`), "", "T")
	xsltExpectText(t, `<r><n>10</n><n>2</n><n>2</n></r>`, xsltTestStylesheet("", `<xsl:for-each select="/r/n"><xsl:sort data-type="number"/><xsl:value-of select="."/><xsl:text>|</xsl:text></xsl:for-each>`), "", "2|2|10|")
	xsltExpectText(t, `<r><n/><n/><n/></r>`, xsltTestStylesheet("", `<xsl:for-each select="/r/n"><xsl:number format="01"/><xsl:text>|</xsl:text></xsl:for-each>`), "", "01|02|03|")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `B<xsl:unknown/>A<xsl:value-of select="2+3"/>`), "", "BA5")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `<xsl:unknown><xsl:fallback>F</xsl:fallback></xsl:unknown>`), "", "F")
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:apply-templates select="1"/>`), "", XSLTApply, "")
}
func TestXSLTXPathEnvironment(t *testing.T) {
	xsltExpectText(t, `<r ref="b"><n id="a">A</n><n id="b">B</n></r>`, xsltTestStylesheet(`<xsl:key name="k" match="n" use="@id"/>`, `<xsl:for-each select="/r"><xsl:value-of select="n[@id=current()/@ref]"/></xsl:for-each>|<xsl:value-of select="key('k','b')"/>`), "", "B|B")
	style := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:d="urn:d" version="1.0"><d:k>S</d:k><xsl:output method="text"/><xsl:key name="k" match="s|d:k" use="'x'"/><xsl:template match="/"><xsl:variable name="source" select="/r/s"/><xsl:for-each select="document('')"><xsl:value-of select="key('k','x')"/>|<xsl:value-of select="name(key('k','x'))"/>|<xsl:value-of select="name(/*)"/>|<xsl:value-of select="generate-id(key('k','x'))=generate-id($source)"/></xsl:for-each></xsl:template></xsl:stylesheet>`
	xsltExpectText(t, `<r><s>A</s></r>`, style, "", "A|s|xsl:stylesheet|true")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `<xsl:variable name="v"><a>A</a><b>B</b></xsl:variable><xsl:value-of select="string($v)"/>|<xsl:value-of select="boolean($v)"/>`), "", "AB|true")
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:variable name="v"><a>A</a></xsl:variable><xsl:value-of select="$v/a"/>`), "", XSLTApply, "Invalid type")
	xsltExpectText(t, `<r>a<![CDATA[b]]>c</r>`, xsltTestStylesheet("", `<xsl:value-of select="count(/r/text())"/>:<xsl:value-of select="/r"/>`), "", "3:abc")
	xsltExpectText(t, `<r> <n> A </n> </r>`, xsltTestStylesheet(`<xsl:strip-space elements="*"/>`, `<xsl:value-of select="count(/r/text())"/>:<xsl:value-of select="/r"/>`), "", "0: A ")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `<xsl:value-of select="format-number(1234.5,'#,##0.00')"/>`), "", "1,234.50")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `<xsl:value-of xmlns="http://www.w3.org/1999/XSL/Transform" select="element-available('value-of')"/>|<xsl:value-of select="function-available('string')"/>|<xsl:value-of select="system-property('xsl:version')"/>|<xsl:value-of select="system-property('xsl:vendor')"/>|<xsl:value-of select="system-property('xsl:vendor-url')"/>`), "", "true|true|1.0|stuntgres|https://github.com/rlewczuk/stuntgres")
	// The nil environment remains the ordinary XPath evaluator: no XSLT variables
	// or extra functions leak into unrelated scalar/table/type operations.
	_, d, err := parseExpanded(context.Background(), "<r>A</r>", Options{Mode: Document})
	if err != nil {
		t.Fatal(err)
	}
	f, err := newXPathTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	expr, err := compileXPath(context.Background(), "current()")
	if err != nil {
		t.Fatal(err)
	}
	f.eval(expr)
	var xp *XPathError
	if !errors.As(f.p.err, &xp) || xp.Detail != "Unregistered function" {
		t.Fatal(f.p.err)
	}
}
func TestXSLTParameters(t *testing.T) {
	top := `<xsl:param name="p" select="'default'"/>`
	for _, tc := range []struct{ params, want string }{{"", "default"}, {"ignored", "default"}, {"p='given'", "given"}, {"p=1+2", "3"}, {"p=name(/*)", "r"}, {"p=position()", "1"}, {"p=last()", "1"}, {"p='a=b'", "a=b"}, {" p='spaced'", "default"}, {"p=1,ignored", "1"}, {"a=1,b=2,c=3,d=4,e=5,f=6,g=7,h=8,i=9,j=10,k=11,p='many'", "many"}} {
		t.Run(tc.params, func(t *testing.T) {
			xsltExpectText(t, "<r/>", xsltTestStylesheet(top, `<xsl:value-of select="$p"/>`), tc.params, tc.want)
		})
	}
	for _, tc := range []struct{ params, detail string }{{"p=", "Invalid expression"}, {"p='x,y'", "Unfinished literal"}, {"unused=[", "Invalid expression"}, {"p=1,p=2", ""}, {"bad:name=1", ""}, {"p=$v", "Undefined variable"}} {
		xsltExpectError(t, "<r/>", xsltTestStylesheet(top, `<xsl:value-of select="$p"/>`), tc.params, XSLTApply, tc.detail)
	}
	// Source-derived distinctions: earlier supplied values are visible, stylesheet
	// globals are not; a declared variable is not a user-parameter binding.
	xsltExpectText(t, "<r/>", xsltTestStylesheet(top, `<xsl:value-of select="$p"/>`), "a=2,p=$a+1", "3")
	xsltExpectText(t, "<r/>", xsltTestStylesheet(`<xsl:variable name="v" select="'V'"/>`, `<xsl:value-of select="$v"/>`), "v=[", "V")
	style := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p" version="1.0"><xsl:param name="p:q" select="'default'"/><xsl:output method="text"/><xsl:template match="/"><xsl:value-of select="$p:q"/></xsl:template></xsl:stylesheet>`
	xsltExpectText(t, "<r/>", style, "{urn:p}q='Q'", "Q")
	xsltExpectText(t, "<r/>", style, "p:q='Q'", "Q")
}
func TestXSLTNoIO(t *testing.T) {
	xsltExpectText(t, `<!DOCTYPE r [<!ENTITY e SYSTEM "file:///nonexistent-stuntgres-xslt">]><r>&e;</r>`, xsltTestStylesheet("", `<xsl:value-of select="/r"/>`), "", "")
	for _, href := range []string{"#missing", "file:///nonexistent-stuntgres-xslt", "https://127.0.0.1:1/missing"} {
		xsltExpectError(t, "<r/>", xsltTestStylesheet(`<xsl:include href="`+href+`"/>`, ""), "", XSLTCompile, "line 1: Document is empty\n\n^")
	}
	xsltExpectError(t, "<r/>", xsltTestStylesheet(`<xsl:include href=""/>`, ""), "", XSLTCompile, "")
	xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:copy-of select="document('file:///nonexistent-stuntgres-xslt')"/>`), "", XSLTApply, "")
	xsltExpectText(t, "<r/>", xsltTestStylesheet("", `<xsl:value-of select="name(document('')/*)"/>`), "", "xsl:stylesheet")
	for _, href := range []string{"file:///tmp/stuntgres-xslt-denied/output.xml", "http://127.0.0.1:9/stuntgres-xslt-denied/output.xml"} {
		xsltExpectError(t, "<r/>", xsltTestStylesheet("", `<xsl:document href="`+href+`">denied</xsl:document>`), "", XSLTApply, "")
	}
}
func TestXSLTCancellation(t *testing.T) {
	source := `<r>` + strings.Repeat(`<n id="x">a</n>`, 100) + `</r>`
	style := xsltTestStylesheet(`<xsl:key name="k" match="n" use="@id"/>`, `<xsl:for-each select="/r/n"><xsl:sort select="."/><xsl:value-of select="count(key('k','x'))"/></xsl:for-each>`)
	for _, phase := range []string{"instruction", "matches", "run", "keyIndex", "sortNodes"} {
		ctx := &phaseContext{Context: context.Background(), phase: phase, after: 2, cause: context.Canceled}
		out, err := ProcessXSLT(ctx, source, style, "")
		if !ctx.admitted || !errors.Is(err, context.Canceled) || out != (XSLTResult{}) {
			t.Fatalf("phase %s admitted=%v result=%+v error=%v", phase, ctx.admitted, out, err)
		}
		xsltExpectText(t, "<r/>", xsltTestStylesheet("", "healthy"), "", "healthy")
	}
}
func TestXSLTOwnership(t *testing.T) {
	resultType := reflect.TypeFor[XSLTResult]()
	if resultType.NumField() != 2 || resultType.Field(0).Type.Kind() != reflect.String || resultType.Field(1).Type.Kind() != reflect.Bool {
		t.Fatal("result retains runtime state")
	}
	style := xsltTestStylesheet(`<xsl:param name="p"/>`, `<xsl:value-of select="$p"/>`)
	retained, err := ProcessXSLT(context.Background(), "<r/>", style, "p='retained'")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	outputs := make([]XSLTResult, 12)
	failures := make([]error, 12)
	for at := range outputs {
		group.Add(1)
		go func(at int) {
			defer group.Done()
			params := "p='B'"
			if at%2 == 0 {
				params = "p='A'"
			}
			outputs[at], failures[at] = ProcessXSLT(context.Background(), "<r/>", style, params)
		}(at)
	}
	group.Wait()
	for at, out := range outputs {
		want := "B"
		if at%2 == 0 {
			want = "A"
		}
		if failures[at] != nil || out.Null || out.Text != want {
			t.Fatal(at, out, failures[at])
		}
	}
	if retained.Text != "retained" || retained.Null {
		t.Fatal("published datum changed", retained)
	}
}
