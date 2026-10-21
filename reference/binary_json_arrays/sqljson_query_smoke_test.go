package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryFeatureSmoke(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT JSON_EXISTS($1::jsonb,'$.a[*] ? (@ == $x)' PASSING $2::int4 AS x) AS present,JSON_QUERY($1::jsonb,'$.a[*]' WITH WRAPPER) AS items,JSON_VALUE($1::jsonb,'$.s' RETURNING int4 DEFAULT -1 ON EMPTY DEFAULT -2 ON ERROR) AS value`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		// Close before native reset: the controls phase owns independent handles.
		func() {
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			ok := func(doc any, row []any) {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"present", "items", "value"}, []string{"BOOL", "JSONB", "INT4"}, [][]any{row}, doc, 2))
			}
			ok(sqljsonQueryDocument, []any{true, `[1, 2]`, int64(123)})
			ok(`{}`, []any{false, nil, int64(-1)})
			ok(`{"a":[3],"s":[1,2]}`, []any{false, `[3]`, int64(-2)})
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
			d.Detail, d.Context = `Token "x" is invalid.`, `JSON data, line 1: x`
			s := fixture.ErrorDiagnostic(q, d, `x`, 2)
			s.SelectRows, s.ErrorStage = true, "execute"
			pg := d
			pg.Context = "JSON data, line 1: x\nunnamed portal parameter $1 = '...'"
			s.PostgresDiagnostic = &pg
			s.Diagnostic = &pg
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			ok(sqljsonQueryDocument, []any{true, `[1, 2]`, int64(123)})
			ok(nil, []any{nil, nil, nil})
		}()
		sqljsonQueryCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_QUERY('{}','$.missing' WITH WRAPPER EMPTY ARRAY ON EMPTY)`, "JSONB", `[]`},
			{`JSON_QUERY('null','$')`, "JSONB", `null`},
			{`JSON_VALUE('null','$' DEFAULT 'empty' ON EMPTY)`, "TEXT", nil},
			{`JSON_QUERY('"123"','$' RETURNING int4 OMIT QUOTES ERROR ON ERROR)`, "INT4", int64(123)},
			{`JSON_QUERY('"123"','$' RETURNING text KEEP QUOTES)`, "TEXT", `"123"`},
		})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW sqjq_smoke AS SELECT JSON_VALUE('{"s":"7"}','$.s' RETURNING int4) AS v`, 0))
		check := sqljsonQueryOne(`SELECT v FROM sqjq_smoke`, "INT4", int64(7))
		sqljsonBoth(t, p, b, c, check)
		jsonpathNavigateReplay(t, p, b, c, check)
	})
}
