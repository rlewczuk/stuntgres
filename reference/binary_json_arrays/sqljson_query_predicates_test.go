package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQuery(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE sqj_source(id int,v int,doc text)`, 0))
		run(fixture.Exec(`INSERT INTO sqj_source VALUES (1,2,'{"n":2}'),(2,NULL,NULL),(3,1,'{"n":1}')`, 3))
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_ARRAY(SELECT v FROM sqj_source ORDER BY id)`, "JSON", `[2, 1]`},
			{`JSON_ARRAY(SELECT v FROM sqj_source WHERE FALSE)`, "JSON", nil},
			{`JSON_ARRAY(SELECT v FROM sqj_source WHERE id=2)`, "JSON", `[]`},
			{`JSON_ARRAY(SELECT NULL::int)`, "JSON", `[]`},
			{`JSON_ARRAY(SELECT v FROM sqj_source ORDER BY id DESC LIMIT 2)`, "JSON", `[1]`},
			{`JSON_ARRAY(VALUES (2),(NULL),(1))`, "JSON", `[2, 1]`},
			{`JSON_ARRAY(WITH x AS (SELECT v,id FROM sqj_source) SELECT v FROM x ORDER BY id)`, "JSON", `[2, 1]`},
			{`JSON_ARRAY(SELECT 2 UNION ALL SELECT 1)`, "JSON", `[2, 1]`},
			{`JSON_ARRAY(SELECT doc FROM sqj_source ORDER BY id FORMAT JSON)`, "JSON", `[{"n":2}, {"n":1}]`},
			{`JSON_ARRAY(SELECT '{}'::jsonb)`, "JSONB", `[{}]`},
			{`JSON_ARRAY(SELECT v FROM sqj_source ORDER BY id RETURNING text)`, "TEXT", `[2, 1]`},
			{`JSON_ARRAY(SELECT v FROM sqj_source ORDER BY id RETURNING bytea)`, "BYTEA", `[2, 1]`},
		})
		t.Run("correlated", func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT o.id, JSON_ARRAY(SELECT i.v FROM sqj_source i WHERE i.id <= o.id ORDER BY i.id) AS v FROM sqj_source o ORDER BY o.id`, []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `[2]`}, {int64(2), `[2]`}, {int64(3), `[2, 1]`}}))
		})
		t.Run("nested correlation", func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT o.id, JSON_ARRAY(SELECT JSON_ARRAY(SELECT i.v FROM sqj_source i WHERE i.id=o.id)) AS v FROM sqj_source o ORDER BY o.id`, []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `[[2]]`}, {int64(2), `[[]]`}, {int64(3), `[[1]]`}}))
		})
		run(fixture.Exec(`CREATE VIEW sqj_query_view AS SELECT o.id, JSON_ARRAY(SELECT i.v FROM sqj_source i WHERE i.id<=o.id ORDER BY i.id) AS v FROM sqj_source o`, 0))
		t.Run("query view", func(t *testing.T) {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT id,v FROM sqj_query_view ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `[2]`}, {int64(2), `[2]`}, {int64(3), `[2, 1]`}}))
		})
	})
}

func TestReferenceSQLJSONPredicates(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			input                        string
			valid, scalar, array, object bool
		}{
			{`{}`, true, false, false, true}, {`[]`, true, false, true, false}, {`"s"`, true, true, false, false},
			{`1`, true, true, false, false}, {`true`, true, true, false, false}, {`false`, true, true, false, false}, {`null`, true, true, false, false},
			{``, false, false, false, false}, {` `, false, false, false, false}, {`{`, false, false, false, false}, {`[1,]`, false, false, false, false},
			{`"unterminated`, false, false, false, false}, {`01`, false, false, false, false}, {`{} x`, false, false, false, false},
		} {
			t.Run(tc.input, func(t *testing.T) {
				for _, kind := range []struct {
					name string
					want bool
				}{{"", tc.valid}, {" VALUE", tc.valid}, {" SCALAR", tc.scalar}, {" ARRAY", tc.array}, {" OBJECT", tc.object}} {
					for _, not := range []bool{false, true} {
						suffix, want := " IS JSON"+kind.name, kind.want
						if not {
							suffix, want = " IS NOT JSON"+kind.name, !want
						}
						t.Run(suffix, func(t *testing.T) {
							fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT $1::text`+suffix+` AS v`, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, tc.input))
						})
					}
				}
			})
		}
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`NULL::text IS JSON`, "BOOL", nil}, {`NULL::text IS NOT JSON SCALAR WITH UNIQUE KEYS`, "BOOL", nil},
			{`'{}'::json IS JSON OBJECT`, "BOOL", true}, {`'[]'::jsonb IS NOT JSON SCALAR`, "BOOL", true},
			{`'\x6e756c6c'::bytea IS JSON SCALAR`, "BOOL", true},
			{`('{}' IS JSON WITH UNIQUE KEYS)`, "BOOL", true},
			{`('{}' IS JSON)::boolean`, "BOOL", true},
			{`('{}' IS JSON) = TRUE`, "BOOL", true},
			{`JSON_ARRAY(('{}' IS JSON))`, "JSON", `[true]`},
			{`JSON_OBJECT(('{}' IS JSON) VALUE 1)`, "JSON", `{"true" : 1}`},
			{`'{"a":1,"a":2}' IS JSON WITH UNIQUE KEYS`, "BOOL", false},
			{`'{"a":1,"\u0061":2}' IS JSON WITH UNIQUE`, "BOOL", false},
			{`'[{"a":1,"a":2}]' IS JSON ARRAY WITH UNIQUE KEYS`, "BOOL", false},
			{`'[{"a":1},{"a":2}]' IS JSON WITH UNIQUE KEYS`, "BOOL", true},
			{`'{"a":1,"a":2}' IS JSON WITHOUT UNIQUE KEYS`, "BOOL", true},
			{`'{"a":1,"a":2}'::jsonb IS JSON WITH UNIQUE KEYS`, "BOOL", true},
			{`'{"a":1,"a":2} x' IS JSON WITH UNIQUE KEYS`, "BOOL", false},
		})
	})
}

func TestReferenceSQLJSONUnique(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_OBJECT('a':1,'a':2 WITHOUT UNIQUE KEYS)`, "JSON", `{"a" : 1, "a" : 2}`},
			{`JSON_OBJECT('a':1,'a':2 RETURNING jsonb)`, "JSONB", `{"a": 2}`},
			{`JSON('[{"a":1},{"a":2}]' WITH UNIQUE KEYS)`, "JSON", `[{"a":1},{"a":2}]`},
			{`JSON('{"a":1,"a":2}' WITHOUT UNIQUE KEYS)`, "JSON", `{"a":1,"a":2}`},
			{`JSON_OBJECT(NULL:NULL ABSENT ON NULL)`, "JSON", `{}`},
		})
		for _, tc := range []struct{ expr, message string }{
			{`JSON_OBJECT(1:1,'1':2 WITH UNIQUE KEYS)`, `duplicate JSON object key value: "1"`},
			{`JSON_OBJECT(1:1,'1':NULL ABSENT ON NULL WITH UNIQUE KEYS)`, `duplicate JSON object key value: "1"`},
			{`JSON_OBJECT(1:1,'1':NULL ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb)`, `duplicate JSON object key value`},
			{`JSON_OBJECT(1:1,'1':2 WITH UNIQUE KEYS RETURNING jsonb FORMAT JSON)`, `duplicate JSON object key value: "1"`},
			{`JSON('{"a":1,"a":2}' WITH UNIQUE KEYS)`, `duplicate JSON object key value`},
			{`JSON('{"a":1,"\u0061":2}' WITH UNIQUE KEYS)`, `duplicate JSON object key value`},
			{`JSON('[{"a":1,"a":2}]' WITH UNIQUE KEYS)`, `duplicate JSON object key value`},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				fixture.SQLJSONDiagnostic(t, p, b, c, "SELECT "+tc.expr+" AS v", fixture.ControlsReferenceDiagnostic("22030", tc.message), false)
			})
		}
		t.Run("null JSON key", func(t *testing.T) {
			fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT JSON_OBJECT(NULL:1) AS v`, fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key"), false)
		})
		t.Run("null JSONB key before omission", func(t *testing.T) {
			fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT JSON_OBJECT(NULL:NULL ABSENT ON NULL RETURNING jsonb) AS v`, fixture.ControlsReferenceDiagnostic("22023", "argument 1: key must not be null"), false)
		})
	})
}
