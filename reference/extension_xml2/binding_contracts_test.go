package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2Missing(signature string) fixture.Diagnostic {
	d := xml2Error("42883", "function "+signature+" does not exist")
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	d.Position = "8"
	return d
}

func xml2BindingErrors(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q, signature string }{
		{"SELECT xml_valid()", "xml_valid()"},
		{"SELECT xml_valid('<r/>','extra')", "xml_valid(unknown, unknown)"},
		{"SELECT xml_valid(1)", "xml_valid(integer)"},
		{"SELECT xpath_string('<r/>')", "xpath_string(unknown)"},
		{"SELECT xpath_number('<r/>','.',1)", "xpath_number(unknown, unknown, integer)"},
		{"SELECT xpath_bool(1,2)", "xpath_bool(integer, integer)"},
		{"SELECT xpath_string(doc => '<r/>',path => '.')", "xpath_string(doc => unknown, path => unknown)"},
		{"SELECT xpath_string(*)", "xpath_string()"},
		{"SELECT xpath_string(VARIADIC ARRAY['<r/>','.'])", "xpath_string(text[])"},
		{"SELECT xpath_number(NULL::text,1::integer)", "xpath_number(text, integer)"},
	} {
		xml2Failure(t, p, b, c, tc.q, xml2Missing(tc.signature), true)
	}
	for _, tc := range []struct{ q, message string }{
		{"SELECT xpath_string(DISTINCT '<r/>','.')", "DISTINCT specified, but xpath_string is not an aggregate function"},
		{"SELECT xpath_string('<r/>','.') FILTER (WHERE true)", "FILTER specified, but xpath_string is not an aggregate function"},
		{"SELECT xpath_string('<r/>','.') OVER ()", "OVER specified, but xpath_string is not a window function nor an aggregate function"},
	} {
		d := xml2Error("42809", tc.message)
		d.Position = "8"
		xml2Failure(t, p, b, c, tc.q, d, true)
	}
	xml2Both(t, p, b, c, fixture.Select("SELECT xpath_string('<r/>'::varchar,'.'::varchar) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{""}}))
}

func xml2Demand(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ fn, typ string }{{"xpath_string", "TEXT"}, {"xpath_number", "FLOAT4"}, {"xpath_bool", "BOOL"}} {
		xml2Failure(t, p, b, c, "SELECT "+tc.fn+"('<r/>','[') AS value WHERE false", xml2Compile("Invalid expression"), false)
		xml2Both(t, p, b, c, fixture.Select("SELECT CASE WHEN false THEN "+tc.fn+"('<r/>','[') ELSE NULL END AS value", []string{"value"}, []string{tc.typ}, [][]any{{nil}}))
	}
}

func xml2Metadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, empty := range []bool{false, true} {
		q := "SELECT xml_valid('<r/>') AS v,xpath_string('<r>12</r>','.') AS s,xpath_number('<r>12</r>','.') AS n,xpath_bool('<r/>','.') AS b"
		values := [][]any{{true, "12", float64(12), true}}
		if empty {
			q += " WHERE false"
			values = nil
		}
		s := fixture.Select(q, []string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, values)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 4 {
				t.Fatal("scalar metadata", meta, err)
			}
			fixture.ScalarAssertMetadata(t, b.PG, meta, 0, 1, 2, 3)
			wantFloat := reflect.TypeFor[float64]()
			if b.PG {
				wantFloat = reflect.TypeFor[float32]()
			}
			if meta[2].ScanType() != wantFloat {
				t.Fatalf("FLOAT4 ScanType=%v want=%v", meta[2].ScanType(), wantFloat)
			}
			for i, col := range meta {
				length, lengthOK := col.Length()
				wantLength, wantLengthOK := int64(0), false
				if i == 1 {
					wantLength, wantLengthOK = math.MaxInt64, true
				}
				if length != wantLength || lengthOK != wantLengthOK {
					t.Fatalf("column%d Length=(%d,%t) want=(%d,%t)", i, length, lengthOK, wantLength, wantLengthOK)
				}
				if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
					t.Fatalf("column%d DecimalSize=(%d,%d,%t)", i, precision, scale, known)
				}
			}
		}
		xml2Both(t, p, b, c, s)
	}
}

func xml2SelectedCoercion(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TYPE xml2_input AS (v integer)",
		"CREATE FUNCTION xml2_cast(xml2_input) RETURNS text LANGUAGE SQL IMMUTABLE CALLED ON NULL INPUT AS 'SELECT ''<r>12</r>''::text'",
		"CREATE CAST (xml2_input AS text) WITH FUNCTION xml2_cast(xml2_input) AS IMPLICIT",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	// The selected non-STRICT cast turns a typed NULL into a real TEXT document
	// before the installed routine's own STRICT rule can inspect its arguments.
	xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid(NULL::xml2_input) AS v,xpath_string(NULL::xml2_input,'.') AS s,xpath_number(NULL::xml2_input,'.') AS n,xpath_bool(NULL::xml2_input,'.') AS b",
		[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{true, "12", float64(12), true}}))
	xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid($1::text) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, nil))
}
