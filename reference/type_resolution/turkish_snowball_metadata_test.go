package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTurkishSnowballMetadata(t *testing.T) {
	t.Run("ordinary-adapter-errors", turkishAdapterErrors)
	t.Run("debug-vector-descriptors", turkishOutputDescriptors)
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"turkish_stem", "kitaplar"}, "{kitap}"}, {[]any{[]byte("turkish_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"turkish_stem", ""}, "{}"}, {[]any{"turkish_stem", []byte{}}, "{}"}, {[]any{"turkish_stem", nil}, nil}, {[]any{nil, "kitaplar"}, nil},
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
				dictionaryResult(t, p, b, e, q, want, "turkish_stem", []byte(nil))
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Turkish Quoted" (TEMPLATE=snowball,Language=turkish)`, 0)
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`SELECT ts_lexize('public."Turkish Quoted"','kitaplar') AS lexemes`, `SELECT ts_lexize('pg_catalog.turkish_stem','kitaplar') AS lexemes`} {
				s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{kitap}"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT ts_lexize('turkish_stem','kitaplar') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { turkishLexizeMetadata(t, b, rows) }
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			held, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			s.Prepared = false
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: s.Query}, b.Recorder, s)
			held.Close()
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT pg_typeof(ts_lexize('turkish_stem','kitaplar'))::oid::bigint AS lexize_oid,pg_typeof(to_tsvector('turkish','ve kitaplar çok evler'))::oid::bigint AS vector_oid", []string{"lexize_oid", "vector_oid"}, []string{"INT8", "INT8"}, [][]any{{int64(1009), int64(3614)}}))
	})
}
