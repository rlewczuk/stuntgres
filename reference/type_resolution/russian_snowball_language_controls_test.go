package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func russianChangedStockLanguage(t *testing.T) {
	var emitted []byte
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer russianStockCleanup(t, p, b, c)()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_stem (language=english)", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range [][2]string{{"машины", "{машины}"}, {"RUNNING", "{run}"}, {"все", "{}"}} {
				portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('russian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='russian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"stopwords = 'russian', language = 'english'"}})
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('russian','машины RUNNING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'run':2 'машины':1"}}))
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native changed-language stream required")
			}
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_stem (language=russian,stopwords=russian)", 0)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual changed-language replay", err)
			}
			check(p, b, c)
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('russian_stem','машины') AS lexemes", "{машин}")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("changed-language redump", err)
			}
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc)
	})
}
