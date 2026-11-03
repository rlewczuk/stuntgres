package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCatalanSnowballMetadata(t *testing.T) {
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
				{[]any{"catalan_stem", "gats"}, "{gat}"}, {[]any{"pg_catalog.catalan_stem", "café"}, "{caf}"}, {[]any{[]byte("catalan_stem"), []byte("ONLY")}, "{only}"}, {[]any{"catalan_stem", ""}, "{}"}, {[]any{"catalan_stem", []byte{}}, "{}"}, {[]any{"catalan_stem", nil}, nil}, {[]any{nil, "gats"}, nil},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
			}
			var want any
			dictionaryResult(t, p, b, c, q, want, "catalan_stem", []byte(nil))
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "catalan_stem", []byte(nil))
			regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('catalan_stem','gats') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

// Only asciiword is overridden in the custom dictionary scenarios.
func catalanDictionaryConfigMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, config, dictionary string) {
	t.Helper()
	var custom, stem, simple int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regdictionary::oid::bigint,'catalan_stem'::regdictionary::oid::bigint,'simple'::regdictionary::oid::bigint", dictionary).Scan(&custom, &stem, &simple); err != nil {
		t.Fatal(err)
	}
	rows := [][]any{}
	for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		dict := simple
		switch id {
		case 1:
			dict = custom
		case 2, 10, 11, 16, 17:
			dict = stem
		}
		rows = append(rows, []any{id, int64(1), dict})
	}
	regtsSelect(t, p, b, c, "SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg=$1::regconfig ORDER BY maptokentype,mapseqno", []string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, rows, config)
}
