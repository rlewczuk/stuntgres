package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2SetsBinding(t *testing.T) {
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		for _, name := range []string{"xpath_nodeset", "xpath_list"} {
			xml2Failure(t, p, b, c, "SELECT "+name+"('<r/>','.')", xml2Missing(name+"(unknown, unknown)"), true)
		}
		xml2Install(t, p, b, c)
		for _, tc := range []struct{ q, signature string }{
			{"SELECT xpath_nodeset()", "xpath_nodeset()"},
			{"SELECT xpath_nodeset('<r/>')", "xpath_nodeset(unknown)"},
			{"SELECT xpath_nodeset('<r/>','.','','','extra')", "xpath_nodeset(unknown, unknown, unknown, unknown, unknown)"},
			{"SELECT xpath_list('<r/>')", "xpath_list(unknown)"},
			{"SELECT xpath_list('<r/>','.','','extra')", "xpath_list(unknown, unknown, unknown, unknown)"},
			{"SELECT xpath_nodeset(1,2)", "xpath_nodeset(integer, integer)"},
			{"SELECT xpath_list(1,2)", "xpath_list(integer, integer)"},
			{"SELECT xpath_nodeset(NULL::text,1::integer)", "xpath_nodeset(text, integer)"},
			{"SELECT xpath_list(NULL::text,1::integer)", "xpath_list(text, integer)"},
			{"SELECT xpath_nodeset(doc => '<r/>',path => '.')", "xpath_nodeset(doc => unknown, path => unknown)"},
			{"SELECT xpath_list(doc => '<r/>',path => '.')", "xpath_list(doc => unknown, path => unknown)"},
			{"SELECT xpath_nodeset(*)", "xpath_nodeset()"},
			{"SELECT xpath_list(*)", "xpath_list()"},
			{"SELECT xpath_nodeset(VARIADIC ARRAY['<r/>','.'])", "xpath_nodeset(text[])"},
			{"SELECT xpath_list(VARIADIC ARRAY['<r/>','.'])", "xpath_list(text[])"},
		} {
			t.Run(tc.signature, func(t *testing.T) { xml2Failure(t, p, b, c, tc.q, xml2Missing(tc.signature), true) })
		}
		for _, name := range []string{"xpath_nodeset", "xpath_list"} {
			for _, tc := range []struct{ expression, message string }{
				{name + "(DISTINCT '<r/>','.')", "DISTINCT specified, but " + name + " is not an aggregate function"},
				{name + "('<r/>','.') FILTER (WHERE true)", "FILTER specified, but " + name + " is not an aggregate function"},
				{name + "('<r/>','.') OVER ()", "OVER specified, but " + name + " is not a window function nor an aggregate function"},
			} {
				d := xml2Error("42809", tc.message)
				d.Position = "8"
				xml2Failure(t, p, b, c, "SELECT "+tc.expression, d, true)
			}
		}
		for _, call := range xml2SetCalls() {
			q := "SELECT " + call.expression + " AS value"
			for _, empty := range []bool{false, true} {
				s := xml2SetValue(q, call.healthy, call.args...)
				if empty {
					s.Query += " WHERE false"
					s.Rows = nil
				}
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 {
						t.Fatal(meta, err)
					}
					fixture.ScalarAssertMetadata(t, b.PG, meta, 0)
					if meta[0].ScanType() != reflect.TypeFor[string]() {
						t.Errorf("TEXT ScanType=%v", meta[0].ScanType())
					}
					if n, ok := meta[0].Length(); n != math.MaxInt64 || !ok {
						t.Errorf("TEXT Length=(%d,%t)", n, ok)
					}
					if precision, scale, ok := meta[0].DecimalSize(); precision != 0 || scale != 0 || ok {
						t.Errorf("TEXT DecimalSize=(%d,%d,%t)", precision, scale, ok)
					}
				}
				xml2Both(t, p, b, c, s)
			}
		}
		xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_nodeset('<r/>'::varchar,'.'::varchar) AS value", "<r/>"))
		xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_list('<r>A</r>'::varchar,'.'::varchar) AS value", "A"))
		for _, q := range []string{
			"CREATE TYPE xml2_sets_input AS (v integer)",
			"CREATE FUNCTION xml2_sets_cast(xml2_sets_input) RETURNS text LANGUAGE SQL IMMUTABLE CALLED ON NULL INPUT AS 'SELECT ''<r><a>A</a></r>''::text'",
			"CREATE CAST (xml2_sets_input AS text) WITH FUNCTION xml2_sets_cast(xml2_sets_input) AS IMPLICIT",
			"CREATE TYPE xml2_sets_null_input AS (v integer)",
			"CREATE FUNCTION xml2_sets_null_cast(xml2_sets_null_input) RETURNS text LANGUAGE SQL IMMUTABLE CALLED ON NULL INPUT AS 'SELECT NULL::text'",
			"CREATE CAST (xml2_sets_null_input AS text) WITH FUNCTION xml2_sets_null_cast(xml2_sets_null_input) AS IMPLICIT",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ name, want string }{{"xpath_nodeset", "<a>A</a>"}, {"xpath_list", "A"}} {
			xml2Both(t, p, b, c, xml2SetValue("SELECT "+tc.name+"(NULL::xml2_sets_input,'/r/a') AS value", tc.want))
			xml2Both(t, p, b, c, xml2SetValue("SELECT "+tc.name+"(ROW(1)::xml2_sets_null_input,'[') AS value", nil))
			// Literal bad XPath is fold-demanded even under WHERE false,
			// with the actual SQL wrapper's complete statement attribution.
			var value any
			err := c.QueryRowContext(p.Ctx, "SELECT "+tc.name+"('<r/>','[') AS value WHERE false").Scan(&value)
			d := xml2SetsSQLContext(xml2Compile("Invalid expression"), tc.name)
			fixture.AssertDiagnostic(t, err, &d)
			xml2Both(t, p, b, c, xml2SetValue("SELECT CASE WHEN false THEN "+tc.name+"('<r/>','[') ELSE NULL END AS value", nil))
		}
	})
}
