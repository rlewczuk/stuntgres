package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTablePolicies(t *testing.T) {
	t.Run("hard boundaries and conversions", jsonTablePolicyBoundaries)
	for _, tc := range []struct {
		name, q     string
		cols, types []string
		rows        [][]any
	}{
		{"scalar defaults", `SELECT * FROM JSON_TABLE('"a"','$' COLUMNS(a int PATH '$' DEFAULT 1 ON EMPTY DEFAULT 2 ON ERROR,b int PATH 'strict $.a' DEFAULT 1 ON EMPTY DEFAULT 2 ON ERROR,c int PATH 'lax $.a' DEFAULT 1 ON EMPTY DEFAULT 2 ON ERROR))`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(2), int64(1)}}},
		{"formatted policies", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a jsonb PATH '$.x' EMPTY ARRAY ON EMPTY,b jsonb PATH '$.x' EMPTY OBJECT ON EMPTY,c text FORMAT JSON PATH '$.x' DEFAULT '7' ON EMPTY,d jsonb PATH 'strict $.x' EMPTY ARRAY ON ERROR,e jsonb PATH 'strict $.x' EMPTY OBJECT ON ERROR,f jsonb PATH '$.x' NULL ON EMPTY))`, []string{"a", "b", "c", "d", "e", "f"}, []string{"JSONB", "JSONB", "TEXT", "JSONB", "JSONB", "JSONB"}, [][]any{{`[]`, `{}`, `7`, `[]`, `{}`, nil}}},
		{"exists policies", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a boolean EXISTS PATH 'strict $.x' TRUE ON ERROR,b boolean EXISTS PATH 'strict $.x' FALSE ON ERROR,c boolean EXISTS PATH 'strict $.x' UNKNOWN ON ERROR,d bigint EXISTS PATH '$' UNKNOWN ON ERROR,e boolean EXISTS PATH '$.x',f text EXISTS PATH '$'))`, []string{"a", "b", "c", "d", "e", "f"}, []string{"BOOL", "BOOL", "BOOL", "INT8", "BOOL", "TEXT"}, [][]any{{true, false, nil, nil, false, `true`}}},
		{"table error independent", `SELECT * FROM JSON_TABLE('"bad"','$' COLUMNS(v int PATH '$') ERROR ON ERROR)`, []string{"v"}, []string{"INT4"}, [][]any{{nil}}},
		{"nested suppressed", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(p FOR ORDINALITY,NESTED 'strict $.x' COLUMNS(v int PATH '$')))`, []string{"p", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}},
		{"root suppressed", `SELECT * FROM JSON_TABLE('{}','strict $.x' COLUMNS(v int PATH '$') EMPTY ARRAY ON ERROR)`, []string{"v"}, []string{"INT4"}, nil},
		{"discard complete path prefix", `SELECT * FROM JSON_TABLE('[{"x":1},{}]','strict $[*].x' COLUMNS(v int PATH '$'))`, []string{"v"}, []string{"INT4"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(tc.q, tc.cols, tc.types, tc.rows))
			})
		})
	}
	for _, tc := range []struct{ name, q, code, message, detail, hint string }{
		{"root error", `SELECT * FROM JSON_TABLE($1::jsonb,'strict $.x' COLUMNS(a int PATH '$') ERROR ON ERROR)`, "2203A", `JSON object does not contain key "x"`, "", ""},
		{"nested error", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(NESTED 'strict $.x' COLUMNS(a int PATH '$')) ERROR ON ERROR)`, "2203A", `JSON object does not contain key "x"`, "", ""},
		{"column error table empty", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(a int PATH '$.x' ERROR ON EMPTY) EMPTY ON ERROR)`, "22035", `no SQL/JSON item found for specified path of column "a"`, "", ""},
		{"empty terminal", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(a int PATH '$.x' ERROR ON EMPTY DEFAULT 7 ON ERROR))`, "22035", `no SQL/JSON item found for specified path of column "a"`, "", ""},
		{"chosen default terminal", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(a int PATH '$.x' DEFAULT (1/0) ON EMPTY DEFAULT 7 ON ERROR))`, "22012", `division by zero`, "", ""},
		{"exists smallint", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(a smallint EXISTS PATH '$.x'))`, "42804", `could not coerce ON ERROR expression (FALSE) to the RETURNING type`, `invalid input syntax for type smallint: "false"`, ""},
		{"formatted multi", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(a jsonb PATH '$.*' ERROR ON ERROR))`, "22034", `JSON path expression for column "a" must return single item when no wrapper is requested`, "", `Use the WITH WRAPPER clause to wrap SQL/JSON items into an array.`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				d.Hint = tc.hint
				doc := `{}`
				if tc.name == "formatted multi" {
					doc = `{"x":1,"y":2}`
				}
				fixture.SQLJSONDiagnostic(t, p, b, c, tc.q, d, false, doc)
			})
		})
	}
	t.Run("unselected and missing subtree defaults", func(t *testing.T) {
		fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_lazy`, 0))
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT * FROM JSON_TABLE('{"x":1}','$' COLUMNS(a int PATH '$.x' DEFAULT nextval('jt_lazy') ON EMPTY DEFAULT nextval('jt_lazy') ON ERROR,NESTED '$.missing' COLUMNS(b int PATH '$.x' DEFAULT nextval('jt_lazy') ON EMPTY)))`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.SQLJSONQueryOne(`SELECT nextval('jt_lazy') AS v`, "INT8", int64(1)))
		})
	})
}
