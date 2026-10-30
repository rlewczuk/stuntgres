package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLTableDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, row, doc, columns, code, message, detail string
	}{
		{"fragment context", "/r", `<a/><b/>`, `v text`, "2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^"},
		{"multiple scalar nodes", "/r", `<r><v>A</v><v>B</v></r>`, `v text`, "21000", "more than one value returned by column XPath expression", ""},
		{"multiple text nodes", "/r", `<r><v>A<b/>B</v></r>`, `v text PATH 'v/text()'`, "21000", "more than one value returned by column XPath expression", ""},
		{"mixed text and CDATA scalar", "/r", `<r>T&amp;<![CDATA[A<B]]>U&amp;</r>`, `v text PATH 'text()'`, "21000", "more than one value returned by column XPath expression", ""},
		{"separated CDATA scalar", "/r", `<r><![CDATA[A]]><!--boundary--><![CDATA[B]]></r>`, `v text PATH 'text()'`, "21000", "more than one value returned by column XPath expression", ""},
		{"missing not null", "/r", `<r/>`, `v int NOT NULL`, "22004", `null is not allowed in column "v"`, ""},
		{"null default not null", "/r", `<r/>`, `v int DEFAULT NULL NOT NULL`, "22004", `null is not allowed in column "v"`, ""},
		{"null column filter", "/r", `<r/>`, `v int PATH NULL`, "22004", "column filter expression must not be null", `Filter for column "v" is null.`},
		{"empty row", "", `<r/>`, `v text`, "10608", "row path filter must not be empty string", ""},
		{"empty column", "/r", `<r/>`, `v text PATH ''`, "10608", "column path filter must not be empty string", ""},
		{"invalid row", "[", `<r/>`, `v text`, "10608", "invalid XPath expression", "Invalid expression"},
		{"invalid column", "/r", `<r/>`, `v text PATH '['`, "10608", "invalid XPath expression", "Invalid expression"},
		{"undefined row prefix", "/p:r", `<r/>`, `v text`, "10608", "could not create XPath object", "Undefined namespace prefix"},
		{"undefined column prefix", "/r", `<r/>`, `v text PATH 'p:v'`, "10608", "could not create XPath object", "Undefined namespace prefix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := `SELECT * FROM XMLTABLE(($1::text) PASSING ($2::xml) COLUMNS ` + tc.columns + `)`
				fixture.SQLJSONDiagnostic(t, p, b, c, q, xmlTableDiagnostic(tc.code, tc.message, tc.detail), false, tc.row, tc.doc)
			})
		})
	}
	for _, tc := range []struct{ name, q, token, code, message string }{
		{"duplicate column", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int, v text)`, "v text", "42601", `column name "v" is not unique`},
		{"duplicate namespace", `SELECT * FROM XMLTABLE(XMLNAMESPACES('urn:1' AS p, 'urn:2' AS p), '/r' PASSING '<r/>' COLUMNS v int)`, "'urn:2'", "42601", `namespace name "p" is not unique`},
		{"second ordinal", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS n FOR ORDINALITY, m FOR ORDINALITY)`, "m FOR", "42601", "only one FOR ORDINALITY column is allowed"},
		{"setof column", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v SETOF int)`, "v SETOF", "42P16", `column "v" cannot be declared SETOF`},
		{"duplicate default", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int DEFAULT 1 DEFAULT 2)`, "DEFAULT 2", "42601", "only one DEFAULT value is allowed"},
		{"duplicate path", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int PATH 'v' PATH 'w')`, "PATH 'w'", "42601", "only one PATH value per column is allowed"},
		{"conflicting null", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int NULL NOT NULL)`, "NOT NULL", "42601", `conflicting or redundant NULL / NOT NULL declarations for column "v"`},
		{"redundant null", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int NULL NULL)`, "NULL)", "42601", `conflicting or redundant NULL / NOT NULL declarations for column "v"`},
		{"default namespace", `SELECT * FROM XMLTABLE(XMLNAMESPACES(DEFAULT 'urn:r'), '/r' PASSING '<r/>' COLUMNS v int)`, "DEFAULT", "0A000", "DEFAULT namespace is not supported"},
		{"missing passing", `SELECT * FROM XMLTABLE('/r' '<r/>' COLUMNS v int)`, "'<r/>'", "42601", `syntax error at or near "'<r/>'"`},
		{"missing columns", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>')`, ")", "42601", `syntax error at or near ")"`},
		{"parenthesized columns", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS(v int))`, "(v", "42601", `syntax error at or near "("`},
		{"bare row cast", `SELECT * FROM XMLTABLE('/r'::text PASSING '<r/>' COLUMNS v int)`, "::", "42601", `syntax error at or near "::"`},
		{"bare document cast", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>'::xml COLUMNS v int)`, "::", "42601", `syntax error at or near "::"`},
		{"bare row operator", `SELECT * FROM XMLTABLE('/' || 'r' PASSING '<r/>' COLUMNS v int)`, "||", "42601", `syntax error at or near "||"`},
		{"trailing junk", `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int); garbage`, "garbage", "42601", `syntax error at or near "garbage"`},
	} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			t.Run(tc.name+prefix, func(t *testing.T) {
				resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := prefix + tc.q
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					idx := strings.LastIndex(q, tc.token)
					if tc.name != "default namespace" { d.Position = fixture.Position(len([]rune(q[:idx])) + 1) }
					fixture.SQLJSONDiagnostic(t, p, b, c, q, d, tc.name != "default namespace")
				})
			})
		}
	}
	t.Run("null runtime row", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT * FROM XMLTABLE(($1::text) PASSING '<r/>' COLUMNS v int)`, xmlTableDiagnostic("22004", "row filter expression must not be null", ""), false, nil)
		})
	})
	t.Run("bind inactive default", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := `SELECT * FROM XMLTABLE('/r' PASSING (NULL::xml) COLUMNS v int DEFAULT xmltable_no_such_routine())`
			d := fixture.ControlsReferenceDiagnostic("42883", "function xmltable_no_such_routine() does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = fixture.Position(strings.Index(q, "xmltable_no_such_routine") + 1)
			fixture.SQLJSONDiagnostic(t, p, b, c, q, d, true)
		})
	})
	t.Run("default namespace runtime precedence",func(t *testing.T){
		resultsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			q:=`SELECT * FROM XMLTABLE(XMLNAMESPACES(DEFAULT 'urn:r'), '/r' PASSING ($1::xml) COLUMNS v int)`
			fixture.SQLJSONBoth(t,p,b,c,fixture.Select(q,[]string{"v"},[]string{"INT4"},nil,nil))
			fixture.SQLJSONDiagnostic(t,p,b,c,q,xmlTableDiagnostic("2200M","could not parse XML document","line 1: Extra content at the end of the document\n<a/><b/>\n    ^"),false,`<a/><b/>`)
			q=`SELECT * FROM XMLTABLE(XMLNAMESPACES(DEFAULT $1::text), '[' PASSING '<r/>' COLUMNS v int PATH '[')`
			fixture.SQLJSONDiagnostic(t,p,b,c,q,xmlTableDiagnostic("22004","namespace URI must not be null",""),false,nil)
		})
	})
}
