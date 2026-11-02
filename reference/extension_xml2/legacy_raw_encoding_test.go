package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2RawEncoding(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct {
			name, body   string
			valid        bool
			text, number any
			boolean      bool
		}{
			{"ascii", "A", true, "A", float64(1), true},
			{"jis_hiragana_a", "\x1b$B$\"\x1b(B", false, "あ", float64(1), true},
			{"invalid_escape", "\x1b$X", false, nil, nil, false},
			{"truncated_jis", "\x1b$B$", false, nil, nil, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				doc := "<?xml version=\"1.0\" encoding=\"ISO-2022-JP\"?><r>" + tc.body + "</r>"
				xml2Both(t, p, b, c, fixture.Select(
					"SELECT xml_valid($1::text) AS v,xpath_string($1::text,'.') AS s,xpath_number($1::text,'string-length(.)') AS n,xpath_bool($1::text,'.') AS b",
					[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"},
					[][]any{{tc.valid, tc.text, tc.number, tc.boolean}}, doc))
			})
		}
	})
}
