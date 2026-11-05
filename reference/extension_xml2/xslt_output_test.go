package reference_test

import (
	"database/sql"
	"encoding/hex"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTOutput(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ name, output, declarations, body, want string }{
			{"xml_default", ``, ``, `<out>&lt;&amp;</out>`, "<?xml version=\"1.0\"?>\n<out>&lt;&amp;</out>\n"},
			{"xml_omit", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<out>&lt;&amp;</out>`, "<out>&lt;&amp;</out>\n"},
			{"xml_properties", `<xsl:output method="xml" version="1.0" encoding="UTF-8" standalone="yes"/>`, ``, `<out/>`, "<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<out/>\n"},
			{"xml_doctype", `<xsl:output omit-xml-declaration="yes" doctype-public="urn:public" doctype-system="urn:system"/>`, ``, `<out/>`, "<!DOCTYPE out PUBLIC \"urn:public\" \"urn:system\">\n<out/>\n"},
			{"xml_indent", `<xsl:output omit-xml-declaration="yes" indent="yes"/>`, ``, `<out><a/><b/></out>`, "<out>\n  <a/>\n  <b/>\n</out>\n"},
			{"mixed_no_indent", `<xsl:output omit-xml-declaration="yes" indent="yes"/>`, ``, `<out>A<a/>B</out>`, "<out>A<a/>B</out>\n"},
			{"multiple_roots", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<a>A</a><b>B</b>`, "<a>A</a><b>B</b>\n"},
			{"rtf_copy_all", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:variable name="v"><a>A</a><b>B</b></xsl:variable><xsl:copy-of select="$v"/>`, "<a>A</a><b>B</b>\n"},
			{"text_nodes_only", xsltTextOutput, ``, `T<xsl:comment>C</xsl:comment><xsl:processing-instruction name="p">D</xsl:processing-instruction><e>E</e>`, "TE"},
			{"comment_pi", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:comment>C</xsl:comment><xsl:processing-instruction name="p">D</xsl:processing-instruction><out/>`, "<!--C-->\n<?p D?><out/>\n"},
			{"cdata_split", `<xsl:output omit-xml-declaration="yes" cdata-section-elements="out"/>`, ``, `<out><xsl:text>A]]&gt;B&lt;&amp;</xsl:text></out>`, "<out><![CDATA[A]]]]><![CDATA[>B<&]]></out>\n"},
			{"disable_escaping", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<out><xsl:text disable-output-escaping="yes">&lt;a/&gt;</xsl:text><xsl:value-of select="'&lt;b/&gt;'" disable-output-escaping="yes"/></out>`, "<out><a/><b/></out>\n"},
			{"avt_escape", `<xsl:output omit-xml-declaration="yes"/>`, `<xsl:param name="p" select="'X'"/>`, `<out a="{{{$p}}}"/>`, "<out a=\"{X}\"/>\n"},
			{"attribute_sets", `<xsl:output omit-xml-declaration="yes"/>`, `<xsl:attribute-set name="a"><xsl:attribute name="x">1</xsl:attribute></xsl:attribute-set><xsl:attribute-set name="b" use-attribute-sets="a"><xsl:attribute name="y">3</xsl:attribute></xsl:attribute-set>`, `<e xsl:use-attribute-sets="b" x="2"/>`, "<e y=\"3\" x=\"2\"/>\n"},
			{"attribute_replacement", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<e a="1"><xsl:attribute name="a">2</xsl:attribute></e>`, "<e a=\"2\"/>\n"},
			{"dynamic_construction", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:element name="out"><xsl:attribute name="a"><xsl:value-of select="'&lt;&amp;'"/></xsl:attribute>T</xsl:element>`, "<out a=\"&lt;&amp;\">T</out>\n"},
			{"dynamic_namespace", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:element name="n:out" namespace="urn:n"><xsl:attribute name="n:a" namespace="urn:n">V</xsl:attribute></xsl:element>`, "<n:out xmlns:n=\"urn:n\" n:a=\"V\"/>\n"},
			{"exclude_namespace", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<out xmlns:u="urn:unused" xsl:exclude-result-prefixes="u"/>`, "<out xmlns:u=\"urn:unused\"/>\n"},
			{"namespace_alias", `<xsl:output omit-xml-declaration="yes"/>`, `<xsl:namespace-alias xmlns:old="urn:old" xmlns="urn:new" stylesheet-prefix="old" result-prefix="#default"/>`, `<old:out xmlns:old="urn:old"/>`, "<old:out xmlns:old=\"urn:new\"/>\n"},
			{"deep_copy", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:copy-of select="r/n"/>`, "<n a=\"1\"><b>B</b></n>\n"},
			{"shallow_copy", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:for-each select="r/n"><xsl:copy><xsl:copy-of select="@*"/></xsl:copy></xsl:for-each>`, "<n a=\"1\"/>\n"},
			{"html_implicit", ``, ``, `<html><br/><input checked="checked"/></html>`, "<html>\n<br><input checked>\n</html>\n"},
			{"html_explicit", `<xsl:output method="html" indent="no"/>`, ``, `<html><br/><input checked="checked"/><script>if (a &lt; b) a++;</script></html>`, "<html><br><input checked><script>if (a < b) a++;</script></html>\n"},
			{"html_head_meta", `<xsl:output method="html" indent="no" encoding="UTF-8" media-type="text/custom"/>`, ``, `<html><head><title>T</title></head><body/></html>`, "<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\"><title>T</title></head><body></body></html>\n"},
			{"html_uri", `<xsl:output method="html" indent="no"/>`, ``, `<a href="é x">é</a>`, "<a href=\"%C3%A9%20x\">é</a>\n"},
			{"output_merge", `<xsl:output method="xml" omit-xml-declaration="yes"/><xsl:output indent="yes"/>`, ``, `<out><a/></out>`, "<out>\n  <a/>\n</out>\n"},
			{"pi_xml", `<xsl:output omit-xml-declaration="yes"/>`, ``, `<xsl:processing-instruction name="xml">D</xsl:processing-instruction>T`, "<?xml D?>T\n"},
			{"implicit_html_cdata", `<xsl:output cdata-section-elements="p"/>`, ``, `<html><p>A</p></html>`, "<html><p>A</p></html>\n"},
			{"explicit_html_cdata", `<xsl:output method="html" cdata-section-elements="p"/>`, ``, `<html><p>A</p></html>`, "<html><p>A</p></html>\n"},
			{"xml_cdata_over_doe", `<xsl:output omit-xml-declaration="yes" cdata-section-elements="p"/>`, ``, `<p><xsl:text disable-output-escaping="yes">&lt;a/&gt;</xsl:text></p>`, "<p><![CDATA[<a/>]]></p>\n"},
			{"namespace_amp_default", ``, ``, `<p:e xmlns:p="urn:n?a=1&amp;b=2"/>`, "<?xml version=\"1.0\"?>\n<p:e xmlns:p=\"urn:n?a=1&b=2\"/>\n"},
			{"namespace_amp_utf8", `<xsl:output encoding="UTF-8"/>`, ``, `<p:e xmlns:p="urn:n?a=1&amp;b=2"/>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<p:e xmlns:p=\"urn:n?a=1&b=2\"/>\n"},
			{"namespace_unicode_default", ``, ``, `<p:e xmlns:p="urn:é"/>`, "<?xml version=\"1.0\"?>\n<p:e xmlns:p=\"urn:é\"/>\n"},
			{"namespace_unicode_utf8", `<xsl:output encoding="UTF-8"/>`, ``, `<p:e xmlns:p="urn:é"/>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<p:e xmlns:p=\"urn:é\"/>\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				xsltExample(t, p, b, c, `<r><n a="1"><b>B</b></n></r>`, xsltSmokeStyle(tc.output, tc.declarations, tc.body), tc.want)
			})
		}
		for _, tc := range []struct{ label, want string }{
			{"UTF-8", "あé"}, {"ISO-2022-JP", "\x1b$B$\x22\x1b(B&#233;"},
			{"ISO-8859-1", "&#12354;\xe9"}, {"not-a-codec", "あé"},
		} {
			t.Run("encoder_"+tc.label, func(t *testing.T) {
				xsltExample(t, p, b, c, `<r/>`, xsltSmokeStyle(`<xsl:output method="text" encoding="`+tc.label+`"/>`, "", `あé`), tc.want)
			})
		}
		for _, tc := range []struct{ label, text, datumHex, pgWireHex string }{
			{"UTF-16", "あé", "fffe4230e900", "fffe4230e9"},
			{"UTF-16LE", "あé", "4230e900", "4230e9"},
			{"UTF-16BE", "あé", "304200e9", "3042"},
			{"UTF-16", "😀", "fffe3dd800de", "fffe3dd8"},
			{"UTF-16LE", "😀", "3dd800de", "3dd8"},
			{"UTF-16BE", "😀", "d83dde00", "d83dde"},
			// Stateful final text is published without an extra encoder flush.
			{"ISO-2022-JP-2", "é", "1b2428442b31", "1b2428442b31"},
			{"ISO-2022-KR", "한", "1b2429430e4751", "1b2429430e4751"},
			{"ISO-2022-CN", "中", "1b2429410e5650", "1b2429410e5650"},
			{"UTF-7", "é", "2b414f", "2b414f"},
		} {
			t.Run(tc.label+tc.text, func(t *testing.T) { xsltDatum(t, p, b, c, tc.label, tc.text, tc.datumHex, tc.pgWireHex) })
		}
		for _, label := range []string{"UTF-8", "UTF-16", "UTF-16LE", "UTF-16BE", "ISO-2022-JP", "ISO-2022-KR", "ISO-2022-CN", "UTF-7"} {
			xsltDatum(t, p, b, c, label, "", "", "")
		}
	})
}

// The common public query checks the native full datum and PostgreSQL's
// independently measured ordinary text-wire field, plus the full SQL length.
// PostgreSQL's separate textsend phase exposes the bytes retained beyond NUL;
// native rows expose those bytes directly, without requiring an ancillary builtin.
func xsltDatum(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, label, text, datumHex, pgWireHex string) {
	t.Helper()
	datum, err := hex.DecodeString(datumHex)
	if err != nil {
		t.Fatal(err)
	}
	rowHex := datumHex
	if b.PG {
		rowHex = pgWireHex
	}
	row, err := hex.DecodeString(rowHex)
	if err != nil {
		t.Fatal(err)
	}
	style := xsltSmokeStyle(`<xsl:output method="text" encoding="`+label+`"/>`, "", text)
	const materialized = "WITH result AS MATERIALIZED (SELECT xslt_process($1::text,$2::text) AS r) "
	q := materialized + "SELECT r,octet_length(r) AS n FROM result"
	s := fixture.Select(q, []string{"r", "n"}, []string{"TEXT", "INT4"}, [][]any{{string(row), int64(len(datum))}}, `<r/>`, style)
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		t.Helper()
		var raw string
		var n int64
		if err := rows.Scan(&raw, &n); err != nil {
			t.Fatal(err)
		}
		if index != 0 || hex.EncodeToString([]byte(raw)) != rowHex || len(raw) != len(row) || n != int64(len(datum)) {
			t.Fatalf("public result row %d: bytes=%x wire/native length=%d datum length=%d; want bytes=%s wire/native length=%d datum length=%d", index, raw, len(raw), n, rowHex, len(row), len(datum))
		}
	}
	xml2Both(t, p, b, c, s)
	if b.PG {
		t.Run("postgres_datum_boundary", func(t *testing.T) {
			q := materialized + "SELECT encode(textsend(r),'hex') AS h,octet_length(r) AS n FROM result"
			xml2Both(t, p, b, c, fixture.Select(q, []string{"h", "n"}, []string{"TEXT", "INT4"}, [][]any{{datumHex, int64(len(datum))}}, `<r/>`, style))
		})
	}
}
