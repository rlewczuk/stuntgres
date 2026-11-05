package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2RawDocument(label, body string) string {
	return `<?xml version="1.0" encoding="` + label + `"?><r>` + body + `</r>`
}

func xml2RawValues(document any, valid, text, length, boolean any) fixture.Step {
	return fixture.Select("SELECT xml_valid($1::text) AS v,xpath_string($1::text,'.') AS s,xpath_number($1::text,'string-length(.)') AS n,xpath_bool($1::text,'.') AS b",
		[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{valid, text, length, boolean}}, document)
}

func TestReferenceXML2ASCIIJP2(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, label := range []string{"ASCII", "US-ASCII", "ascii", "Us-AsCiI"} {
			xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(label, "A"), true, "A", float64(1), true))
			xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(label, "&#233;"), true, "é", float64(1), true))
			// Text validation ignores the declaration; raw scalar input does not.
			xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(label, "é"), true, nil, nil, false))
		}
		for _, label := range []string{"ISO-2022-JP-2", "ISO2022JP2", "CSISO2022JP2", "iso-2022-jp-2"} {
			for _, tc := range []struct { body, text string; valid bool; length float64 }{
				{"A", "A", true, 1},
				{"\x1b(J\\~\x1b(B", "¥‾", false, 2},
				{"\x1b$@$\"\x1b(B", "あ", false, 1},
				{"\x1b$B$\"\x1b(B", "あ", false, 1},
				{"\x1b$(D+1\x1b(B", "é", false, 1},
				{"\x1b$(D\x22\x37\x1b(B", "\uff5e", false, 1},
				{"\x1b$(CGQ\x1b(B", "한", false, 1},
				{"\x1b$AVP\x1b(B", "中", false, 1},
				{"\x1b(I!\x1b(B", "｡", false, 1},
				{"\x1b.A\x1bNi", "é", false, 1},
				{"\x1b.F\x1bNa", "α", false, 1},
				{"\x1b.A\x1bNiA\x1bNq", "éAñ", false, 3},
				{"\x1b.A\x1b$B$\"\x1b(B\x1bNi\x1b$(CGQ\x1b(B\x1bNq", "あé한ñ", false, 4},
				{"\x1b.A\x1b$B$\"\x1bNi$$\x1b(B", "あéい", false, 3},
				{"\x1b.F\x1bNa\x1b.A\x1bNi\x1b.F\x1bNa", "αéα", false, 3},
				{"\x1b$B$\"\n$$\x1b(B", "あ\nい", false, 3},
				{"\x1b.A\x1bNi\n\x1bNi", "é\né", false, 3},
			} {
				xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(label, tc.body), tc.valid, tc.text, tc.length, true))
			}
		}
		for _, doc := range []string{xml2RawDocument("ISO-2022-JP-2", "\x1b$X"), xml2RawDocument("BOGUS", "A")} {
			xml2Both(t, p, b, c, xml2Scalars(doc, "[", nil, nil, false))
		}
		for _, doc := range []any{nil, xml2RawDocument("ASCII", "A"), xml2RawDocument("ISO-2022-JP-2", "\x1b$(D+1\x1b(B"), "<r>"} {
			xml2Both(t, p, b, c, xml2Scalars(doc, nil, nil, nil, nil))
		}
		xml2Both(t, p, b, c, xml2Scalars(nil, "[", nil, nil, nil))
		xml2Both(t, p, b, c, xml2RawValues(nil, nil, nil, nil, nil))
		xml2Both(t, p, b, c, xml2Scalars("", ".", nil, nil, false))
		xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument("ASCII", ""), true, "", float64(0), true))
		xml2Both(t, p, b, c, xml2Scalars(xml2RawDocument("ISO-2022-JP-2", "A"), "/missing", "", nil, false))

		// Actual stylesheet-shaped input is parsed as a document, not compiled
		// or transformed. XPath selects its literal result node independently.
		for _, tc := range []struct { label, body, text string }{
			{"ASCII", "A", "A"}, {"US-ASCII", "A", "A"},
			{"ISO-2022-JP-2", "A", "A"},
			{"ISO-2022-JP-2", "\x1b$(D+1\x1b(B", "é"},
			{"ISO-2022-JP-2", "\x1b$(CGQ\x1b(B", "한"},
		} {
			style := `<?xml version="1.0" encoding="` + tc.label + `"?><xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0"><xsl:template match="/"><out>` + tc.body + `</out></xsl:template></xsl:stylesheet>`
			xml2Both(t, p, b, c, xml2Scalars(style, "/*/*/*", tc.text, nil, true))
		}
		xml2Held(t, p, b, c,
			xml2RawValues(xml2RawDocument("ISO-2022-JP-2", "\x1b.A\x1bNi\x1b$(CGQ\x1b(B\x1bNq"), false, "é한ñ", float64(3), true),
			xml2RawValues(xml2RawDocument("ISO-2022-JP-2", "\x1b$X"), false, nil, nil, false),
			xml2RawValues(xml2RawDocument("ISO-2022-JP-2", "\x1b$(D+1\x1b(B"), false, "é", float64(1), true),
			xml2RawValues(nil, nil, nil, nil, nil),
			xml2RawValues(xml2RawDocument("US-ASCII", "A"), true, "A", float64(1), true),
			xml2RawValues(xml2RawDocument("ISO-2022-JP-2", "\x1b$(CGQ\x1b(B"), false, "한", float64(1), true))
	})
}
