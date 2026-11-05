package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTBuiltinContext(t *testing.T) {
	// Independently qualified on pinned PostgreSQL 18.6 (review report69).
	// xsltDefaultProcessOneNode counts every real child, but CDATA retains
	// ambient position/size. Explicit apply-templates supplies its own list.
	const start = `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:output method="text"/>`
	const text = `<xsl:template match="text()"><xsl:value-of select="concat(name(..),':',.,':',position(),'/',last(),';')"/></xsl:template>`
	const end = `</xsl:stylesheet>`
	builtin := start + text + end
	explicit := start + `<xsl:template match="/"><xsl:apply-templates select="r/node()"/></xsl:template>` + text + end
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ name, source, builtin, explicit string }{
			{"text_cdata_text", `<r>A<![CDATA[B]]>C</r>`, "r:A:1/3;r:B:1/3;r:C:3/3;", "r:A:1/3;r:B:2/3;r:C:3/3;"},
			{"first_cdata", `<r><![CDATA[B]]>C</r>`, "r:B:1/1;r:C:2/2;", "r:B:1/2;r:C:2/2;"},
			{"last_cdata", `<r>A<![CDATA[B]]></r>`, "r:A:1/2;r:B:1/2;", "r:A:1/2;r:B:2/2;"},
			{"only_cdata", `<r><![CDATA[B]]></r>`, "r:B:1/1;", "r:B:1/1;"},
			{"element_before_text", `<r><n/>A<![CDATA[B]]>C</r>`, "r:A:2/4;r:B:2/4;r:C:4/4;", "r:A:2/4;r:B:3/4;r:C:4/4;"},
			{"unmatched_comment_pi", `<r><!--c--><![CDATA[B]]><?p d?>C</r>`, "r:B:1/1;r:C:4/4;", "r:B:2/4;r:C:4/4;"},
			{"nested_restore", `<r><n>X<![CDATA[Y]]>Z</n><![CDATA[B]]>C</r>`, "n:X:1/3;n:Y:1/3;n:Z:3/3;r:B:1/3;r:C:3/3;", "n:X:1/3;n:Y:1/3;n:Z:3/3;r:B:2/3;r:C:3/3;"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// Metadata, exact non-NULL bytes and absence of notices use the
				// shared public fixture; each stylesheet runs direct and prepared.
				xml2Both(t, p, b, c, xsltValue(tc.source, builtin, tc.builtin))
				xml2Both(t, p, b, c, xsltValue(tc.source, explicit, tc.explicit))
			})
		}
	})
}
