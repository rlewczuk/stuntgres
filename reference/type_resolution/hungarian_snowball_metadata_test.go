package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHungarianSnowballMetadata(t *testing.T) {
	t.Run("ordinary-adapter-errors", hungarianAdapterErrors)
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"hungarian_stem", "házak"}, "{ház}"}, {[]any{[]byte("hungarian_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"hungarian_stem", ""}, "{}"}, {[]any{"hungarian_stem", []byte{}}, "{}"}, {[]any{"hungarian_stem", nil}, nil}, {[]any{nil, "házak"}, nil},
			} {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
					dictionaryResult(t, p, b, e, q, tc.want, tc.args...)
				}
			}
			var want any = "{}"
			if b.PG {
				want = nil
			}
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
				dictionaryResult(t, p, b, e, q, want, "hungarian_stem", []byte(nil))
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Hungarian Quoted" (TEMPLATE=snowball,Language=hungarian)`, 0)
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`SELECT ts_lexize('public."Hungarian Quoted"','házak') AS lexemes`, `SELECT ts_lexize('pg_catalog.hungarian_stem','házak') AS lexemes`} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ház}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT ts_lexize('hungarian_stem','házak') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { hungarianLexizeMetadata(t, b, rows) }
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
	})
}
