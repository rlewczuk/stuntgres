package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLTableScalarAndTypeInput(t *testing.T) {
	t.Run("scalar kinds", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS t int PATH 'true()', f int PATH 'false()', word text PATH 'true()', sum int PATH '1+2', empty text PATH 'string(/missing)' DEFAULT 'fallback', escaped xml PATH '"<&"')`
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"t", "f", "word", "sum", "empty", "escaped"}, []string{"INT4", "INT4", "TEXT", "INT4", "TEXT", "XML"}, [][]any{{int64(1), int64(0), "true", int64(3), "", `&lt;&amp;`}}))
			q = `SELECT * FROM XMLTABLE('/r' PASSING '<r><a>{1,2}</a><b>true</b><v>ab</v></r>' COLUMNS a int[] PATH 'a', b bool PATH 'b', v varchar(2) PATH 'v')`
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"a", "b", "v"}, []string{"_INT4", "BOOL", "VARCHAR"}, [][]any{{`{1,2}`, true, "ab"}}))
			// XML target text input still honors the physical session's xmloption.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET xmloption TO DOCUMENT`, 0))
			q = `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v xml PATH 'v/text()', x xml PATH '"<&"')`
			// Node text and escaped XPath scalars use the existing independent
			// DOCUMENT text-input diagnostic contract.
			fixture.SQLJSONDiagnostic(t,p,b,c,q,xmlTableDiagnostic("2200M","invalid XML document","line 1: Start tag expected, '<' not found\nAB\n^"),false,`<r><v>A<b/>B</v></r>`)
			for _, tc := range []struct{path,detail string}{
				{`'v/text()'`,"line 1: Start tag expected, '<' not found\nAB\n^"},
				{`'"<&"'`,"line 1: Start tag expected, '<' not found\n&lt;&amp;\n^"},
			} {
				q=`SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v xml PATH `+tc.path+`)`
				fixture.SQLJSONDiagnostic(t,p,b,c,q,xmlTableDiagnostic("2200M","invalid XML document",tc.detail),false,`<r><v>A<b/>B</v></r>`)
			}
		})
	})
	for _, tc := range []struct{ name, doc, declaration, code, message string }{
		{"integer syntax", `<r><v>bad</v></r>`, `v int`, "22P02", `invalid input syntax for type integer: "bad"`},
		{"integer overflow", `<r><v>2147483648</v></r>`, `v int`, "22003", `value "2147483648" is out of range for type integer`},
		{"typmod input", `<r><v>abc</v></r>`, `v varchar(2)`, "22001", `value too long for type character varying(2)`},
		{"default is not recovery", `<r><v>bad</v></r>`, `v int DEFAULT 42`, "22P02", `invalid input syntax for type integer: "bad"`},
		{"empty is not missing", `<r><v/></r>`, `v int DEFAULT 42`, "22P02", `invalid input syntax for type integer: ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS `+tc.declaration+`)`, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false, tc.doc)
			})
		})
	}
	t.Run("domains and null precedence", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				`CREATE DOMAIN xt_positive AS integer CHECK (VALUE > 0)`,
				`CREATE DOMAIN xt_required AS integer NOT NULL`,
				`CREATE DOMAIN xt_content AS xml`,
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			q := `SELECT v::int AS v FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v xt_positive)`
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, `<r><v>7</v></r>`))
			d := fixture.ControlsReferenceDiagnostic("23514", `value for domain xt_positive violates check constraint "xt_positive_check"`)
			d.Schema, d.DataType, d.Constraint = "public", "xt_positive", "xt_positive_check"
			fixture.SQLJSONDiagnostic(t, p, b, c, q, d, false, `<r><v>-1</v></r>`)
			fixture.SQLJSONBoth(t,p,b,c,fixture.Select(`SELECT v::int AS v FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v xt_required)`,[]string{"v"},[]string{"INT4"},[][]any{{nil}},`<r/>`))
			fixture.SQLJSONDiagnostic(t,p,b,c,`SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v xt_required NOT NULL)`,xmlTableDiagnostic("22004",`null is not allowed in column "v"`,""),false,`<r/>`)
			for _, declaration := range []string{`v xt_required DEFAULT NULL`, `v xt_required DEFAULT NULL NOT NULL`} {
				d := fixture.ControlsReferenceDiagnostic("23502", `domain xt_required does not allow null values`)
				d.Schema, d.DataType = "public", "xt_required"
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS `+declaration+`)`, d, false, `<r/>`)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET xmloption TO DOCUMENT`, 0))
			q = `SELECT v::xml AS v FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v xt_content PATH 'v')`
			fixture.SQLJSONDiagnostic(t,p,b,c,q,xmlTableDiagnostic("21000","more than one value returned by column XPath expression",""),false,`<r><v>A</v><v>B</v></r>`)
		})
	})
	t.Run("session input", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v date)`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct { setting, canonical string }{{"ISO, MDY", "2024-01-02"}, {"ISO, DMY", "2024-02-01"}, {"ISO, MDY", "2024-01-02"}} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET DateStyle TO '`+tc.setting+`'`, 0))
				s := fixture.Select(q, []string{"v"}, []string{"DATE"}, [][]any{{fixture.DateExpected(b.PG,tc.canonical)}}, `<r><v>01/02/2024</v></r>`)
				fixture.DateRunStep(t,p.Ctx,c,b.Recorder,s)
				fixture.DateRunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
				fixture.DateRunStep(t,p.Ctx,held,b.Recorder,s)
			}
		})
	})
}
