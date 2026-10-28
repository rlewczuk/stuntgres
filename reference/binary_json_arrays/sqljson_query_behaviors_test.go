package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryBehaviors(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			behavior string
			want     any
		}{{"", false}, {"FALSE", false}, {"TRUE", true}, {"UNKNOWN", nil}} {
			t.Run("exists "+tc.behavior, func(t *testing.T) {
				clause := ""
				if tc.behavior != "" {
					clause = tc.behavior + " ON ERROR"
				}
				fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(`SELECT JSON_EXISTS('{}','strict $.x' `+clause+`) AS v`, "BOOL", tc.want))
			})
		}
		for _, tc := range []struct {
			behavior string
			want     any
		}{
			{"NULL", nil}, {"DEFAULT NULL", nil}, {"EMPTY", `[]`}, {"EMPTY ARRAY", `[]`}, {"EMPTY OBJECT", `{}`}, {`DEFAULT '7'::jsonb`, `7`},
		} {
			for _, branch := range []string{"EMPTY", "ERROR"} {
				t.Run("query "+tc.behavior+" on "+branch, func(t *testing.T) {
					path := `$.missing`
					if branch == "ERROR" {
						path = `strict $.missing`
					}
					q := `SELECT JSON_QUERY('{}','` + path + `' ` + tc.behavior + ` ON ` + branch + `) AS v`
					fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(q, "JSONB", tc.want))
				})
			}
		}
		for _, branch := range []string{"EMPTY", "ERROR"} {
			for _, tc := range []struct {
				behavior string
				want     any
			}{{"NULL", nil}, {"DEFAULT NULL", nil}, {"DEFAULT 8", int64(8)}} {
				t.Run("value "+tc.behavior+" on "+branch, func(t *testing.T) {
					path := `$.missing`
					if branch == "ERROR" {
						path = `strict $.missing`
					}
					q := `SELECT JSON_VALUE('{}','` + path + `' RETURNING int4 ` + tc.behavior + ` ON ` + branch + `) AS v`
					fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(q, "INT4", tc.want))
				})
			}
		}
		sqljsonQueryCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_QUERY('[{"x":1},{}]','strict $[*].x' WITH WRAPPER EMPTY ARRAY ON ERROR)`, "JSONB", `[]`},
			{`JSON_VALUE('[{"x":1},{}]','strict $[*].x' RETURNING int4 DEFAULT -2 ON ERROR)`, "INT4", int64(-2)},
			{`JSON_QUERY('[1,2]','$[*]' EMPTY OBJECT ON ERROR)`, "JSONB", `{}`},
			{`JSON_VALUE('{"x":1}','$' DEFAULT 'object' ON ERROR)`, "TEXT", `object`},
		})
		// The side effect is observable independently of the selected value. Run
		// each route against a fresh sequence state, including unselected branches.
		run(fixture.Exec(`CREATE SEQUENCE sqjq_default_calls`, 0))
		for _, prepared := range []bool{false, true} {
			for _, fn := range []string{"JSON_VALUE", "JSON_QUERY"} {
				for _, tc := range []struct {
					doc, path string
					want      int64
					called    bool
				}{
					{`1`, `$`, 1, false}, {`{}`, `$.x`, 1, true}, {`{}`, `strict $.x`, 1, true},
				} {
					t.Run(fn+tc.path+tc.doc+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						run(fixture.SQLJSONQueryOne(`SELECT setval('sqjq_default_calls',1,false) AS v`, "INT8", int64(1)))
						q := `SELECT ` + fn + `($1::jsonb,$2::jsonpath RETURNING bigint DEFAULT nextval('sqjq_default_calls') ON EMPTY DEFAULT nextval('sqjq_default_calls') ON ERROR) AS v`
						s := fixture.SQLJSONQueryOne(q, "INT8", tc.want, tc.doc, tc.path)
						s.Prepared = prepared
						run(s)
						run(fixture.Select(`SELECT last_value,is_called FROM sqjq_default_calls`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), tc.called}}))
					})
				}
			}
		}
		for _, fn := range []string{"JSON_VALUE", "JSON_QUERY"} {
			for _, clause := range []string{`NULL ON ERROR`, `DEFAULT '9' ON ERROR`} {
				t.Run(fn+clause, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("22035", "no SQL/JSON item found for specified path")
					fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`($1::jsonb,'$.x' ERROR ON EMPTY `+clause+`) AS v`, d, false, `{}`)
				})
			}
			t.Run(fn+" selected default failure", func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				q := `SELECT ` + fn + `($1::jsonb,'$.x' RETURNING bigint DEFAULT (nextval('sqjq_default_calls')/0) ON EMPTY DEFAULT 9 ON ERROR) AS v`
				fixture.SQLJSONDiagnostic(t, p, b, c, q, d, false, `{}`)
			})
		}
	})
}
