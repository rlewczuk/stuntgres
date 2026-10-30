package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLTableEmptyAndInitialization(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		args []any
	}{
		{"null document", `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, []any{nil}},
		{"no nodes", `SELECT * FROM XMLTABLE('/missing' PASSING '<r/>' COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, nil},
		{"false row", `SELECT * FROM XMLTABLE('false()' PASSING '<r/>' COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, nil},
		{"true row", `SELECT * FROM XMLTABLE('true()' PASSING '<r/>' COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, nil},
		{"numeric row", `SELECT * FROM XMLTABLE('1' PASSING '<r/>' COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, nil},
		{"string row", `SELECT * FROM XMLTABLE('"nonempty"' PASSING '<r/>' COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, nil},
		{"null document suppresses row and path", `SELECT * FROM XMLTABLE(($1::text) PASSING ($2::xml) COLUMNS n FOR ORDINALITY, v int PATH $3::text DEFAULT (1 / (nextval('xmltable_demand_seq') - 1)))`, []any{"[", nil, "["}},
		{"null document suppresses namespace", `SELECT * FROM XMLTABLE(XMLNAMESPACES($1::text AS p), '/p:r' PASSING ($2::xml) COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, []any{nil, nil}},
		{"null document suppresses failing namespace", `SELECT * FROM XMLTABLE(XMLNAMESPACES((1 / (nextval('xmltable_demand_seq') - 1))::text AS p), '/p:r' PASSING ($1::xml) COLUMNS n FOR ORDINALITY, v int DEFAULT 42)`, []any{nil}},
		{"empty rows suppress default", `SELECT * FROM XMLTABLE('/missing' PASSING ($1::xml) COLUMNS n FOR ORDINALITY, v int DEFAULT (1 / (nextval('xmltable_demand_seq') - 1)))`, []any{`<r/>`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE xmltable_demand_seq`, 0))
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(tc.query, []string{"n", "v"}, []string{"INT4", "INT4"}, nil, tc.args...))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT last_value, is_called FROM xmltable_demand_seq`, []string{"last_value","is_called"}, []string{"INT8","BOOL"}, [][]any{{int64(1),false}}))
			})
		})
	}
	for _, tc := range []struct {
		name, query string
		args []any
		diagnostic fixture.Diagnostic
	}{
		{"compile all column filters before rows", `SELECT * FROM XMLTABLE('/missing' PASSING ($1::xml) COLUMNS v text PATH '[')`, []any{`<r/>`}, xmlTableDiagnostic("10608", "invalid XPath expression", "Invalid expression")},
		{"fragment before empty row path", `SELECT * FROM XMLTABLE('' PASSING ($1::xml) COLUMNS v text PATH '[')`, []any{`<a/><b/>`}, xmlTableDiagnostic("2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^")},
		{"namespace before invalid row", `SELECT * FROM XMLTABLE(XMLNAMESPACES($1::text AS p), '[' PASSING ($2::xml) COLUMNS v text PATH '[')`, []any{nil, `<r/>`}, xmlTableDiagnostic("22004", "namespace URI must not be null", "")},
		{"row before invalid column", `SELECT * FROM XMLTABLE(($1::text) PASSING ($2::xml) COLUMNS v text PATH '[')`, []any{nil, `<r/>`}, xmlTableDiagnostic("22004", "row filter expression must not be null", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.SQLJSONDiagnostic(t, p, b, c, tc.query, tc.diagnostic, false, tc.args...)
			})
		})
	}
	t.Run("dynamic implicit lateral operands", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := `SELECT x.* FROM (VALUES('urn:r'::text, '/p:r/p:v'::text, '<r xmlns="urn:r"><v>7</v><v>8</v></r>'::xml, '.'::text)) AS d(ns,r,doc,col), XMLTABLE(XMLNAMESPACES(d.ns AS p), d.r PASSING d.doc COLUMNS n FOR ORDINALITY, v int PATH d.col) AS x ORDER BY n`
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(8)}}))
		})
	})
	for _, query := range []string{
		`SELECT * FROM XMLTABLE('/r' PASSING (NULL::xml) COLUMNS v int DEFAULT (1 / 0))`,
		`SELECT * FROM XMLTABLE(XMLNAMESPACES((1 / 0)::text AS p), '/p:r' PASSING (NULL::xml) COLUMNS v int)`,
		`SELECT * FROM XMLTABLE('/missing' PASSING '<r/>' COLUMNS v int DEFAULT (1 / 0))`,
	} {
		t.Run("immutable planner failure", func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.SQLJSONDiagnostic(t,p,b,c,query,xmlTableDiagnostic("22012","division by zero",""),false)
			})
		})
	}
}

func xmlTableDiagnostic(code, message, detail string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail = detail
	return d
}
