package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent literals: RFC1557/1922 repertoire framing, Unicode16 CNS1 facts,
// RFC2152 UTF16/Base64 arithmetic, and the pinned reader qualification described
// in internal/xml/data/README.md. These are not candidate-generated goldens.
func xml2KRCNUTF7Cases() []struct { label, body, text string; valid bool; length float64 } {
	return []struct { label, body, text string; valid bool; length float64 }{
		{"ISO-2022-KR", "\x1b$)C\x0eGQ\x0f", "한", false, 1},
		{"ISO-2022-CN", "\x1b$)A\x0eVP\x0f", "中", false, 1},
		{"UTF-7", "+AOk-", "é", true, 1},
		{"ISO-2022-KR", "\x0eGQ\x1b$)CGQ\x0f", "한한", false, 2},
		{"ISO-2022-CN", "\x1b$)A\x0eVP\x1b$)GVP\x0eVP\x0f", "中中烘", false, 3},
		{"ISO-2022-CN", "\x1b$)G\x0eVP\x1bN!!VP\x0f", "烘乂烘", false, 3},
		{"ISO-2022-CN", "\x1b$)G\x0e\x27\x28\x27\x2f\x27\x34\x0f", "亠冫勹", false, 3},
		{"UTF-7", "+-", "+", true, 1},
		{"UTF-7", "+AOkDsw-", "éγ", true, 2},
		{"UTF-7", "A+AOk-B+-C", "AéB+C", true, 5},
		{"UTF-7", "+2D3eAA-", "😀", true, 1},
		{"UTF-7", "+AOk", "é", true, 1},
		{"UTF-7", "+!", "!", true, 1},
	}
}

func xml2KRCNUTF7Style(label, body string) string {
	return `<?xml version="1.0" encoding="` + label + `"?><xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="1.0"><xsl:template match="/"><out>` + body + `</out></xsl:template></xsl:stylesheet>`
}

func xml2KRCNUTF7StyleValues(document any, valid, text, length, boolean any) fixture.Step {
	return fixture.Select("SELECT xml_valid($1::text) AS v,xpath_string($1::text,'/*/*/*') AS s,xpath_number($1::text,'string-length(/*/*/*)') AS n,xpath_bool($1::text,'/*/*/*') AS b",
		[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{valid, text, length, boolean}}, document)
}

func TestReferenceXML2KRCNUTF7(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		// Only canonical labels and ASCII case variants are admitted here.
		// Optional aliases require whole-corpus qualification, not one witness.
		for _, tc := range xml2KRCNUTF7Cases() {
			labels := []string{tc.label}
			switch tc.label {
			case "ISO-2022-KR": labels = append(labels, "iso-2022-kr", "IsO-2022-Kr")
			case "ISO-2022-CN": labels = append(labels, "iso-2022-cn", "IsO-2022-Cn")
			case "UTF-7": labels = append(labels, "utf-7", "uTf-7")
			}
			for _, label := range labels {
				xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(label, tc.body), tc.valid, tc.text, tc.length, true))
				// This parses an independently owned stylesheet-shaped document
				// and selects literal out. It does not compile or transform XSLT.
				xml2Both(t, p, b, c, xml2KRCNUTF7StyleValues(xml2KRCNUTF7Style(label, tc.body), tc.valid, tc.text, tc.length, true))
			}
		}
		for _, label := range []string{"ISO-2022-KR", "ISO-2022-CN", "UTF-7"} {
			for range 2 {
				xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(label, ""), true, "", float64(0), true))
				xml2Both(t, p, b, c, xml2Scalars(xml2RawDocument(label, "12"), "/missing", "", nil, false))
			}
		}
		xml2Both(t, p, b, c, xml2RawValues(nil, nil, nil, nil, nil))
		xml2Both(t, p, b, c, xml2Scalars("", ".", nil, nil, false))
	})
}
