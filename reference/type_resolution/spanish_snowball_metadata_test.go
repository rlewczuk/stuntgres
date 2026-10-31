package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSpanishSnowballMetadata(t *testing.T) {
	t.Run("ordinary-adapter-errors", spanishAdapterErrors)
	t.Run("debug-vector-descriptors", spanishOutputDescriptors)
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"spanish_stem", "casas"}, "{cas}"}, {[]any{[]byte("spanish_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"spanish_stem", ""}, "{}"}, {[]any{"spanish_stem", []byte{}}, "{}"}, {[]any{"spanish_stem", nil}, nil}, {[]any{nil, "casas"}, nil},
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
				dictionaryResult(t, p, b, e, q, want, "spanish_stem", []byte(nil))
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Spanish Quoted" (TEMPLATE=snowball,Language=spanish)`, 0)
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`SELECT ts_lexize('public."Spanish Quoted"','casas') AS lexemes`, `SELECT ts_lexize('pg_catalog.spanish_stem','casas') AS lexemes`} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{cas}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT ts_lexize('spanish_stem','casas') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { spanishLexizeMetadata(t, b, rows) }
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
	})
}
