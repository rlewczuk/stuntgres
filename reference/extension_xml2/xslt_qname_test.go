package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTQNames(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		const source = `<r><n/></r>`
		const output = `<xsl:output omit-xml-declaration="yes"/>`
		// Independently observed PostgreSQL 18.6 outcomes: static and named
		// QNames fail compilation; AVT QNames fail only when applied. All
		// exposed diagnostic fields and the absence of notices are exact.
		for _, tc := range []struct{ name, declarations, body, stage string }{
			{"static_element", ``, `<xsl:element name=":out"/>`, "compile"},
			{"static_attribute", ``, `<e><xsl:attribute name=":a">V</xsl:attribute></e>`, "compile"},
			{"dynamic_element", ``, `<xsl:element name="{':out'}"/>`, "apply"},
			{"dynamic_attribute", ``, `<e><xsl:attribute name="{':a'}">V</xsl:attribute></e>`, "apply"},
			{"named_template", `<xsl:template name=":n">N</xsl:template>`, `<xsl:call-template name=":n"/>`, "compile"},
			{"named_variable", `<xsl:variable name=":v" select="'V'"/>`, `T`, "compile"},
			{"named_parameter", `<xsl:param name=":p" select="'D'"/>`, `T`, "compile"},
			{"named_mode", `<xsl:template match="n" mode=":m">N</xsl:template>`, `<xsl:apply-templates select="r/n" mode=":m"/>`, "compile"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(output, tc.declarations, tc.body)
				xml2Failure(t, p, b, c, xsltThreeQuery, xsltDiagnostic(tc.stage, ""), false, source, style, "")
				// A rejected name must not poison a subsequent healthy invocation.
				xml2Both(t, p, b, c, xsltParamValue(source, xsltSmokeStyle(output, "", `<xsl:element name="out"/>`), "", "<out/>\n"))
			})
		}
		for _, tc := range []struct{ name, declarations, body, params, want string }{
			{"valid_unqualified", ``, `<xsl:element name="out"/>`, "", "<out/>\n"},
			{"valid_bound", ``, `<xsl:element xmlns:p="urn:p" name="p:out"/>`, "", "<p:out xmlns:p=\"urn:p\"/>\n"},
			{"valid_dynamic", ``, `<xsl:element xmlns:p="urn:p" name="{'p:out'}"/>`, "", "<p:out xmlns:p=\"urn:p\"/>\n"},
			// SQL parameter names use the released user-parameter splitter, not
			// stylesheet QName validation. :p does not alias the declared p.
			{"sql_parameter_leading_colon", `<xsl:param name="p" select="'D'"/>`, `<xsl:value-of select="$p"/>`, ":p='X'", "D\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(output, tc.declarations, tc.body)
				xml2Both(t, p, b, c, xsltParamValue(source, style, tc.params, tc.want))
			})
		}
	})
}
