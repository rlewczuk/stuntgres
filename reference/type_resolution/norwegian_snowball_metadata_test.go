package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNorwegianSnowballMetadata(t *testing.T) {
	t.Run("ordinary-adapter-errors", norwegianAdapterErrors)
	t.Run("debug-vector-descriptors", norwegianOutputDescriptors)
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
				{[]any{"norwegian_stem", "biler"}, "{bil}"}, {[]any{[]byte("norwegian_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"norwegian_stem", ""}, "{}"}, {[]any{"norwegian_stem", []byte{}}, "{}"}, {[]any{"norwegian_stem", nil}, nil}, {[]any{nil, "biler"}, nil},
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
				dictionaryResult(t, p, b, e, q, want, "norwegian_stem", []byte(nil))
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Norwegian Quoted" (TEMPLATE=snowball,Language=norwegian)`, 0)
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`SELECT ts_lexize('public."Norwegian Quoted"','biler') AS lexemes`, `SELECT ts_lexize('pg_catalog.norwegian_stem','biler') AS lexemes`} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{bil}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT ts_lexize('norwegian_stem','biler') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { norwegianLexizeMetadata(t, b, rows) }
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
	})
}
