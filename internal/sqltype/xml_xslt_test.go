package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestXML2XSLTAdapter(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	const prefix = `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`
	const suffix = `</xsl:stylesheet>`
	style := func(output, body string) string {
		return prefix + output + `<xsl:template match="/">` + body + `</xsl:template>` + suffix
	}
	for _, tc := range []struct{ stylesheet, params, want string }{
		{style(`<xsl:output method="text"/>`, `<xsl:value-of select="/r"/>`), "", "owned"},
		{style(`<xsl:output method="text"/>`, ""), "", ""},
		{prefix + `<xsl:output method="text"/><xsl:param name="p" select="'default'"/><xsl:template match="/"><xsl:value-of select="$p"/></xsl:template>` + suffix, "p='given'", "given"},
		{style(`<xsl:output method="text" encoding="UTF-16LE"/>`, `<xsl:text>あé</xsl:text>`), "", "\x42\x30\xe9\x00"},
	} {
		v, err := XML2XSLT(ctx, types[Text], `<r>owned</r>`, tc.stylesheet, tc.params)
		text, textErr := v.Text()
		if err != nil || textErr != nil || v.IsNull() || !v.ValidResult(types[Text].Usage()) || text != tc.want {
			t.Fatalf("owned TEXT result=%x want=%x null=%v error=%v/%v", text, tc.want, v.IsNull(), err, textErr)
		}
		// A later invocation must not mutate the retained complete datum.
		_, err = XML2XSLT(ctx, types[Text], `<r>later</r>`, style(`<xsl:output method="text"/>`, `<xsl:value-of select="/r"/>`), "")
		after, _ := v.Text()
		if err != nil || after != tc.want {
			t.Fatal("later invocation changed retained output", err)
		}
	}
	for _, tc := range []struct{ source, stylesheet, params, code, primary, detail string }{
		{"", "<bad/>", "", "2200M", "error parsing XML document", ""},
		{"<r>", "<bad/>", "", "2200M", "error parsing XML document", "line 1: Premature end of data in tag r line 1\n<r>\n   ^"},
		{"<r/>", "", "", "2200M", "error parsing stylesheet as XML document", ""},
		{"<r/>", "<bad/>", "", "10608", "failed to parse stylesheet", ""},
		{"<r/>", style("", `<xsl:value-of select="["/>`), "", "10608", "failed to parse stylesheet", "Invalid expression"},
		{"<r/>", style("", `<xsl:value-of select="$missing"/>`), "", "10608", "failed to apply stylesheet", "Undefined variable"},
		{"<r/>", style("", ""), "unused=[", "10608", "failed to apply stylesheet", "Invalid expression"},
	} {
		v, err := XML2XSLT(ctx, types[Text], tc.source, tc.stylesheet, tc.params)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.primary, Detail: tc.detail}
		if v.Type() != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("complete staged SQL diagnostic", err, want)
		}
	}
	for _, descriptor := range []*Descriptor{nil, types[Bool]} {
		if v, err := XML2XSLT(ctx, descriptor, `<r/>`, style("", ""), ""); err == nil || v.Type() != nil {
			t.Fatal("invalid XSLT descriptor admitted")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if v, err := XML2XSLT(cancelled, types[Text], `<r/>`, style("", ""), ""); !errors.Is(err, context.Canceled) || v.Type() != nil {
		t.Fatal("cancellation projected as SQL failure or result", v, err)
	}
}
