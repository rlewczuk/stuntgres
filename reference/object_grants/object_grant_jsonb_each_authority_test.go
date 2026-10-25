package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Pinned PostgreSQL 18.0 declares OIDs 3208/3932 as strict immutable,
// parallel-safe SETOF RECORD functions with one JSONB input. The OUT fields
// are key TEXT and value JSONB/TEXT, not additional GRANT input formals.
func TestReferenceObjectGrantJSONBEachAuthority(t *testing.T) {
	for _, name := range []string{"jsonb_each", "jsonb_each_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_jsonb_each_source(id INT,j JSONB)", 0))
				run(fixture.Exec(`INSERT INTO og_jsonb_each_source VALUES(1,'{"a":1,"a":2,"n":null,"s":"x","d":2.50}'),(2,'{}'),(3,NULL),(4,'null'),(5,'[]')`, 5))
				run(fixture.Exec("GRANT SELECT ON og_jsonb_each_source TO og_object_reader", 0))
				run(fixture.Exec("CREATE VIEW og_jsonb_each_view AS SELECT e.key,e.value FROM og_jsonb_each_source s CROSS JOIN LATERAL "+name+"(s.j) AS e WHERE s.id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_jsonb_each_view TO og_object_reader", 0))

				valueType, sibling, siblingType := "JSONB", "jsonb_each_text", "TEXT"
				var nullValue any = "null"
				stringValue := `"x"`
				if name == "jsonb_each_text" {
					valueType, sibling, siblingType = "TEXT", "jsonb_each", "JSONB"
					nullValue, stringValue = nil, "x"
				}
				const input = `{"a":1,"a":2,"n":null,"s":"x","d":2.50}`
				pairs := [][]any{{"a", "2"}, {"d", "2.50"}, {"n", nullValue}, {"s", stringValue}}
				selectPairs := func(query string, rows [][]any, args ...any) fixture.Step {
					s := fixture.Select(query, []string{"key", "value"}, []string{"TEXT", valueType}, rows, args...)
					s.Bag = true
					return s
				}
				rowQuery := "SELECT e.key,e.value FROM og_jsonb_each_source s CROSS JOIN LATERAL " + name + "(s.j) AS e"
				nonObject := func(id string) fixture.Step {
					s := fixture.ImplicitError(rowQuery+" WHERE s.id="+id, fixture.Diagnostic{Code: "22023", Message: "cannot call " + name + " on a non-object"})
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				cases := []objectGrantProviderCase{
					// These row-backed and stored consumers retain the SAME held
					// statements across PUBLIC revoke, actor grant and actor revoke.
					{selectPairs(rowQuery+" WHERE s.id=1", pairs), true, true},
					{selectPairs("SELECT e.key,e.value FROM og_jsonb_each_source s CROSS JOIN LATERAL pg_catalog."+name+"(from_json=>s.j) AS e WHERE s.id=1", pairs), true, true},
					{selectPairs("SELECT key,value FROM og_jsonb_each_view", pairs), true, true},
					{selectPairs("SELECT ("+name+"(j)).key AS key,("+name+"(j)).value AS value FROM og_jsonb_each_source WHERE id=1", pairs), true, true},

					// Canonical JSONB keeps the last duplicate and numeric scale;
					// JSON null is a JSONB value, but becomes SQL NULL in TEXT mode.
					// Constant-only immutable calls use fresh execution, not a held
					// PostgreSQL plan that might have folded the provider already.
					{selectPairs("SELECT key,value FROM "+name+"($1::JSONB)", pairs, input), true, false},
					{selectPairs("SELECT (e).key AS key,(e).value AS value FROM "+name+"(from_json=>$1::JSONB) AS e", pairs, input), true, false},
					{fixture.Select("SELECT "+name+"('{\"a\":1}'::JSONB) AS r", []string{"r"}, []string{"RECORD"}, [][]any{{"(a,1)"}}), true, false},

					// SRF initialization is not scalar strict-NULL pruning. A
					// surviving FROM or target SRF needs EXECUTE even for zero rows.
					{selectPairs(rowQuery+" WHERE s.id=2", nil), true, true},
					{selectPairs(rowQuery+" WHERE s.id=3", nil), true, true},
					{selectPairs(rowQuery+" WHERE s.id=99", nil), true, true},
					{selectPairs("SELECT key,value FROM "+name+"('{}'::JSONB)", nil), true, false},
					{selectPairs("SELECT key,value FROM "+name+"(NULL::JSONB)", nil), true, false},
					{selectPairs("SELECT key,value FROM "+name+"($1::JSONB)", nil, nil), true, false},
					{fixture.Select("SELECT "+name+"(NULL::JSONB) AS r", []string{"r"}, []string{"RECORD"}, nil), true, false},
					{fixture.Select("SELECT "+name+"(j) AS r FROM og_jsonb_each_source WHERE FALSE", []string{"r"}, []string{"RECORD"}, nil), true, true},

					// SQL NULL documents above yield no rows. JSON null and arrays
					// are non-object values: admitted execution raises exact 22023;
					// denied execution raises 42501 before inspecting the document.
					{nonObject("4"), true, true},
					{nonObject("5"), true, true},

					// Static FROM pruning removes initialization entirely, unlike
					// a surviving empty/data-zero SRF or target WHERE FALSE.
					{selectPairs(rowQuery+" WHERE FALSE", nil), false, true},
					{selectPairs("SELECT key,value FROM "+name+"('null'::JSONB) WHERE FALSE", nil), false, false},
				}
				controls := []fixture.Step{
					fixture.Select("SELECT key,value FROM "+sibling+"('{\"ok\":7}'::JSONB)", []string{"key", "value"}, []string{"TEXT", siblingType}, [][]any{{"ok", "7"}}),
					fixture.Select("SELECT key,value FROM json_each('{\"a\":1,\"a\":2}'::JSON)", []string{"key", "value"}, []string{"TEXT", "JSON"}, [][]any{{"a", "1"}, {"a", "2"}}),
				}
				controls[1].Bag = true
				// OUT key/value describe RECORD shape; only JSONB is an IN formal.
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+name+"(jsonb)", "function", name, cases, controls)
			})
		})
	}
}
