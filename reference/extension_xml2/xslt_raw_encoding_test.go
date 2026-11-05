package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTRawEncoding(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ label, payload, want string }{
			{"ASCII", "A", "A"}, {"us-ascii", "B", "B"},
			{"ISO-2022-JP", "\x1b$B$\x22\x1b(B", "あ"},
			{"ISO-2022-JP-2", "\x1b$(D+1\x1b(B", "é"},
			{"ISO-2022-JP-2", "\x1b$(CGQ\x1b(B", "한"},
			{"ISO-2022-KR", "\x1b$)C\x0eGQ\x0f", "한"},
			{"ISO-2022-CN", "\x1b$)A\x0eVP\x0f", "中"},
			{"UTF-7", "+AOk-", "é"},
		} {
			t.Run(tc.label+tc.want, func(t *testing.T) {
				header := `<?xml version="1.0" encoding="` + tc.label + `"?>`
				source := header + `<r>` + tc.payload + `</r>`
				style := header + xsltSmokeStyle(xsltTextOutput, "", `<xsl:text>`+tc.payload+`</xsl:text>`)
				xsltExample(t, p, b, c, source, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="/r"/>`), tc.want)
				xsltExample(t, p, b, c, `<r/>`, style, tc.want)
				xml2Held(t, p, b, c, xsltValue(source, style, tc.want), xsltValue(`<r>plain</r>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="/r"/>`), "plain"))
			})
		}
		// Different codecs must coexist in one invocation without sharing decoder
		// designation, source namespaces, or a previous invocation's pending bytes.
		xsltExample(t, p, b, c, `<?xml version="1.0" encoding="ISO-2022-KR"?><r>`+"\x1b$)C\x0eGQ\x0f"+`</r>`, `<?xml version="1.0" encoding="UTF-7"?>`+xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="r"/>|+AOk-`), "한|é")
		for _, tc := range []struct{ label, tail string }{{"ASCII", "é"}, {"ISO-2022-KR", "é"}, {"ISO-2022-CN", "é"}, {"UTF-7", "+3AA-"}, {"UTF-7", "+A"}} {
			header := `<?xml version="1.0" encoding="` + tc.label + `"?>`
			style := xsltSmokeStyle(xsltTextOutput, "", `<xsl:text>OK</xsl:text>`)
			xsltExample(t, p, b, c, header+`<r/>`+tc.tail, style, "OK")
			xsltExample(t, p, b, c, `<r/>`, header+style+tc.tail, "OK")
		}
		// A genuinely decoded epilog is not a recoverable converter tail. The
		// release's context window ends immediately before that extra character.
		style := `<?xml version="1.0" encoding="UTF-7"?>` + xsltSmokeStyle(xsltTextOutput, "", `<xsl:text>OK</xsl:text>`)
		window := `><xsl:template match="/"><xsl:text>OK</xsl:text></xsl:template></xsl:stylesheet>`
		xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("style", "line 1: Extra content at the end of the document\n"+window+"\n"+strings.Repeat(" ", 79)+"^"), false, `<r/>`, style+"+AOk-")
		// BOM handling is independent for both actual argument readers.
		xsltExample(t, p, b, c, "\ufeff<r>BOM</r>", "\ufeff"+xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="/r"/>`), "BOM")
		xsltExample(t, p, b, c, `<!DOCTYPE r [<!ATTLIST r ordinary CDATA "D" xmlns:p CDATA "urn:p">]><r><p:n>A</p:n></r>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="count(r/@ordinary)"/>|<xsl:value-of select="namespace-uri(r/*)"/>|<xsl:value-of select="r"/>`), "0|urn:p|A")
	})
}
