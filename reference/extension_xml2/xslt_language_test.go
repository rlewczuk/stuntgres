package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTLanguage(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ name, source, declarations, body, want string }{
			{"strip_view", `<r> <n> A </n> </r>`, `<xsl:strip-space elements="*"/>`, `<xsl:value-of select="count(r/text())"/>|<xsl:value-of select="r"/>`, "0| A "},
			{"preserve_specificity", `<r> <n> A </n> </r>`, `<xsl:strip-space elements="*"/><xsl:preserve-space elements="r"/>`, `<xsl:value-of select="count(r/text())"/>|<xsl:value-of select="r"/>`, "2|  A  "},
			{"xml_space", `<r xml:space="preserve"> <n/> </r>`, `<xsl:strip-space elements="*"/>`, `<xsl:value-of select="count(r/text())"/>`, "0"},
			{"stylesheet_whitespace", `<r/>`, ``, `   <xsl:text> X </xsl:text>   `, " X "},
			{"conditional_demand", `<r><n>A</n><n>B</n></r>`, ``, `<xsl:if test="false()"><xsl:value-of select="$missing"/></xsl:if><xsl:for-each select="r/n"><xsl:choose><xsl:when test="position()=1">F</xsl:when><xsl:otherwise>L</xsl:otherwise></xsl:choose><xsl:value-of select="."/></xsl:for-each>`, "FALB"},
			{"numeric_stable_multikey", `<r><n v="10" id="a"/><n v="2" id="b"/><n v="2" id="c"/></r>`, ``, `<xsl:for-each select="r/n"><xsl:sort select="@v" data-type="number"/><xsl:value-of select="@id"/></xsl:for-each>`, "bca"},
			{"sort_secondary_and_position", `<r><n v="2" id="b"/><n v="1" id="c"/><n v="2" id="a"/></r>`, ``, `<xsl:for-each select="r/n"><xsl:sort select="@v" data-type="number" order="descending"/><xsl:sort select="@id"/><xsl:value-of select="concat(position(), @id)"/></xsl:for-each>`, "1a2b3c"},
			{"sort_original_context", `<r><n>A</n><n>B</n><n>C</n></r>`, ``, `<xsl:apply-templates select="r/n"><xsl:sort select="position()" data-type="number" order="descending"/></xsl:apply-templates>`, "CBA"},
			{"sort_nan_ascending", `<r><n v="bad" id="a"/><n v="2" id="b"/><n v="bad" id="c"/><n v="1" id="d"/></r>`, ``, `<xsl:for-each select="r/n"><xsl:sort select="@v" data-type="number"/><xsl:value-of select="@id"/></xsl:for-each>`, "acdb"},
			{"sort_nan_descending", `<r><n v="bad" id="a"/><n v="2" id="b"/><n v="bad" id="c"/><n v="1" id="d"/></r>`, ``, `<xsl:for-each select="r/n"><xsl:sort select="@v" data-type="number" order="descending"/><xsl:value-of select="@id"/></xsl:for-each>`, "bdac"},
			{"number_single", `<r><n/><n/><n/></r>`, ``, `<xsl:for-each select="r/n"><xsl:number format="01"/><xsl:text>/</xsl:text></xsl:for-each>`, "01/02/03/"},
			{"number_multiple", `<r><s><n/><n/></s><s><n/></s></r>`, ``, `<xsl:for-each select="r/s/n"><xsl:number level="multiple" count="s|n" format="1.1"/><xsl:text>/</xsl:text></xsl:for-each>`, "1.1/1.2/2.1/"},
			{"number_any_from", `<r><s><n/><n/></s><s><n/></s></r>`, ``, `<xsl:for-each select="r/s/n"><xsl:number level="any" count="n" from="s"/><xsl:text>/</xsl:text></xsl:for-each>`, "1/2/1/"},
			{"number_alphabetic_roman_grouping", `<r/>`, ``, `<xsl:number value="27" format="A"/>|<xsl:number value="14" format="i"/>|<xsl:number value="12345" grouping-separator="," grouping-size="3"/>`, "AA|xiv|12,345"},
			{"lexical_binding", `<r/>`, `<xsl:param name="p" select="'G'"/><xsl:template name="n"><xsl:param name="p" select="'D'"/><xsl:value-of select="$p"/></xsl:template>`, `<xsl:call-template name="n"><xsl:with-param name="p" select="'X'"/></xsl:call-template><xsl:value-of select="$p"/>`, "XG"},
			{"global_dependency", `<r/>`, `<xsl:variable name="a" select="concat($b,'A')"/><xsl:variable name="b" select="'B'"/>`, `<xsl:value-of select="$a"/>`, "BA"},
			{"local_scope", `<r/>`, `<xsl:variable name="v" select="'G'"/><xsl:template name="n"><xsl:value-of select="$v"/></xsl:template>`, `<xsl:variable name="v" select="'L'"/><xsl:value-of select="$v"/><xsl:call-template name="n"/>`, "LG"},
			{"message_no_notice", `<r/>`, ``, `<xsl:message>M</xsl:message>T`, "T"},
			{"attribute_cycle", `<r/>`, `<xsl:attribute-set name="a" use-attribute-sets="b"/><xsl:attribute-set name="b" use-attribute-sets="a"/>`, `<e xsl:use-attribute-sets="a"/>`, ""},
			{"pi_xml_text", `<r/>`, ``, `<xsl:processing-instruction name="xml">D</xsl:processing-instruction>T`, "T"},
			{"number_nan", `<r/>`, ``, `<xsl:number value="number('x')"/>`, "NaN"},
			{"number_negative", `<r/>`, ``, `<xsl:number value="-2"/>`, "0"},
			{"number_infinity", `<r/>`, ``, `<xsl:number value="1 div 0"/>`, "Infinity"},
			{"decimal_conflict", `<r/>`, `<xsl:decimal-format name="d" decimal-separator="."/><xsl:decimal-format name="d" decimal-separator=","/>`, `<xsl:value-of select="format-number(1.5,'0.0','d')"/>`, "1.5"},
			{"arabic_zero_digit", `<r/>`, `<xsl:decimal-format name="ar" zero-digit="٠"/>`, `<xsl:value-of select="format-number(12.5,'٠.٠','ar')"/>`, "ÚÛ.Þ"},
			{"format_quotes", `<r/>`, ``, `<xsl:value-of select="format-number(12,&quot;'ab'0&quot;)"/>`, "ab012"},
			// Released decimal overflow recovery emits 499 zeroes on either
			// side of the point; this is not the candidate's float rendering.
			{"format_huge", `<r/>`, ``, `<xsl:value-of select="format-number(number('1e308'),'0.00')"/>`, strings.Repeat("0", 499) + "." + strings.Repeat("0", 499)},
			{"format_bad_mask", `<r/>`, ``, `<xsl:value-of select="format-number(1.25,'0#')"/>`, "1.25"},
			{"format_negative_section", `<r/>`, ``, `<xsl:value-of select="format-number(-12,'0;0')"/>`, "12"},
			{"number_zero_mask", `<r/>`, ``, `<xsl:number value="7" format="00"/>|<xsl:number value="7" format="01"/>`, "7|07"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				xsltExample(t, p, b, c, tc.source, xsltSmokeStyle(xsltTextOutput, tc.declarations, tc.body), tc.want)
			})
		}
		source := `<r><n v="a" id="a"/><n v="A" id="A"/><n v="á" id="acute"/><n v="ä" id="dia"/><n v="aa" id="aa"/><n v="a-" id="a-dash"/><n v="-a" id="dash-a"/><n v="æ" id="ae"/><n v="b" id="b"/><n v="B" id="B"/><n v="z" id="z"/></r>`
		const byteOrder = "dash-a|A|B|a|a-dash|aa|b|z|acute|dia|ae|"
		const englishOrder = "dash-a|a|a-dash|A|acute|dia|aa|ae|b|B|z|"
		for _, tc := range []struct{ name, options, want string }{
			{"default", ``, byteOrder},
			{"en", `lang="en"`, englishOrder},
			{"en_US_tag", `lang="en-US"`, englishOrder},
			{"de", `lang="de"`, byteOrder},
			{"en_GB_tag", `lang="en-GB"`, englishOrder},
			{"eo", `lang="eo"`, byteOrder},
			{"en_US_locale", `lang="en_US"`, byteOrder},
			{"en_upper", `lang="en-US" case-order="upper-first"`, englishOrder},
			{"en_lower", `lang="en-US" case-order="lower-first"`, englishOrder},
			{"default_upper", `case-order="upper-first"`, byteOrder},
			{"default_lower", `case-order="lower-first"`, byteOrder},
		} {
			t.Run("sort_locale_"+tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(xsltTextOutput, "", `<xsl:for-each select="r/n"><xsl:sort select="@v" `+tc.options+`/><xsl:value-of select="@id"/>|</xsl:for-each>`)
				xsltExample(t, p, b, c, source, style, tc.want)
			})
		}
		for _, tc := range []struct{ name, source, lang, want string }{
			{"canonical", `<r><n v="é" id="composed"/><n v="e&#x301;" id="decomposed"/><n v="e" id="plain"/><n v="É" id="upper"/></r>`, "en", "plain|decomposed|composed|upper|"},
			{"undefined", `<r><n v="&#x379;" id="b"/><n v="&#x378;" id="a"/><n v="&#x10FFFF;" id="max"/><n v="&#x1F600;" id="smile"/></r>`, "en-US", "b|a|max|smile|"},
			{"contractions", `<r><n v="l·" id="two-l"/><n v="ŀ" id="one-l"/><n v="И&#x306;" id="two-cyr"/><n v="Й" id="one-cyr"/></r>`, "en-US", "two-l|one-l|two-cyr|one-cyr|"},
		} {
			t.Run("sort_"+tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(xsltTextOutput, "", `<xsl:for-each select="r/n"><xsl:sort select="@v" lang="`+tc.lang+`"/><xsl:value-of select="@id"/>|</xsl:for-each>`)
				xsltExample(t, p, b, c, tc.source, style, tc.want)
			})
		}
		// Recovery is specific to the released instruction, not a generic catch.
		for _, version := range []string{"1.0", "1.1"} {
			prefix := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:e="urn:e" extension-element-prefixes="e" version="` + version + `">` + xsltTextOutput + `<xsl:template match="/">`
			suffix := `</xsl:template></xsl:stylesheet>`
			xsltExample(t, p, b, c, `<r/>`, prefix+`B<xsl:unknown/>A<xsl:value-of select="2+3"/>`+suffix, "BA5")
			xsltExample(t, p, b, c, `<r/>`, prefix+`<e:unknown><xsl:fallback>F</xsl:fallback></e:unknown>`+suffix, "F")
			xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("apply", ""), false, `<r/>`, prefix+`B<e:unknown/>A`+suffix)
		}
		xsltExample(t, p, b, c, `<r/>`, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.1">`+xsltTextOutput+`<xsl:template match="/"><xsl:unknown/></xsl:template></xsl:stylesheet>`, "")
	})
}
