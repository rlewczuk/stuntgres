package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONParameters(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ string
			args   []any
			want   any
		}{
			{`SELECT JSON_ARRAY($1 FORMAT JSON) AS v`, "JSON", []any{`{"n":1}`}, `[{"n":1}]`},
			{`SELECT JSON_OBJECT(($1::text) VALUE $2 FORMAT JSON) AS v`, "JSON", []any{"a", `[1]`}, `{"a" : [1]}`},
			{`SELECT JSON($1) AS v`, "JSON", []any{` {"n":1} `}, ` {"n":1} `},
			{`SELECT JSON_SCALAR($1) AS v`, "JSON", []any{"1"}, `"1"`},
			{`SELECT JSON_SERIALIZE($1) AS v`, "TEXT", []any{`[1]`}, `[1]`},
			{`SELECT $1 IS JSON OBJECT AS v`, "BOOL", []any{`{}`}, true},
			{`SELECT JSON_OBJECT(($1::text) VALUE $1::text) AS v`, "JSON", []any{"a"}, `{"a" : "a"}`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				sqljsonBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...))
			})
		}
		for _, tc := range []struct {
			q, code, position string
			args              []any
		}{
			{`SELECT JSON_OBJECT($1 VALUE $2 FORMAT JSON) AS v`, "42P18", "", []any{"a", `[1]`}},
			{`SELECT JSON_OBJECT($1 VALUE $1) AS v`, "42P08", "20", []any{"a"}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, "could not determine data type of parameter $1")
				d.Position = tc.position
				sqljsonDiagnostic(t, p, b, c, tc.q, d, true, tc.args...)
			})
		}
		for _, q := range []string{`SELECT JSON_ARRAY($1 FORMAT JSON) AS v`, `SELECT JSON_OBJECT('a':$1 FORMAT JSON) AS v`, `SELECT JSON($1 WITH UNIQUE KEYS) AS v`} {
			t.Run("held "+q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				want := `{}`
				if q == `SELECT JSON_ARRAY($1 FORMAT JSON) AS v` {
					want = `[{}]`
				}
				if q == `SELECT JSON_OBJECT('a':$1 FORMAT JSON) AS v` {
					want = `{"a" : {}}`
				}
				ok := func(input any, v any) {
					fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"v"}, []string{"JSON"}, [][]any{{v}}, input))
				}
				ok(`{}`, want)
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail, d.Context = `Token "x" is invalid.`, "JSON data, line 1: x"
				s := fixture.ErrorDiagnostic(q, d, "x")
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				if q == `SELECT JSON($1 WITH UNIQUE KEYS) AS v` {
					d = fixture.ControlsReferenceDiagnostic("22030", "duplicate JSON object key value")
					s = fixture.ErrorDiagnostic(q, d, `{"a":1,"a":2}`)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				}
				var null any
				if q == `SELECT JSON_ARRAY($1 FORMAT JSON) AS v` {
					null = `[]`
				}
				if q == `SELECT JSON_OBJECT('a':$1 FORMAT JSON) AS v` {
					null = `{"a" : null}`
				}
				ok(nil, null)
				ok(`{}`, want)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE sqj_held(id int,v int)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO sqj_held VALUES (1,2),(2,NULL),(3,1)`, 3))
		t.Run("held query", func(t *testing.T) {
			q := `SELECT JSON_ARRAY(SELECT v FROM sqj_held WHERE id <= $1 ORDER BY id) AS v`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				n    int
				want any
			}{{1, `[2]`}, {3, `[2, 1]`}, {0, nil}, {2, `[2]`}, {3, `[2, 1]`}} {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"v"}, []string{"JSON"}, [][]any{{tc.want}}, tc.n))
			}
		})
	})
}
