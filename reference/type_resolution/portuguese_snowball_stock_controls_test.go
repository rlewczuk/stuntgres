package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The PostgreSQL phase consumes the actual native bytes, never equivalent handwritten SQL.
func portugueseRemovedStockControls(t *testing.T) {
	var emitted []byte
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer portugueseStockCleanup(t, p, b, c)()
		q := "SELECT ts_lexize('portuguese_stem',$1::text) AS lexemes"
		dictionaryResult(t, p, b, c, q, "{}", "não")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_stem (stopwords)", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range [][2]string{{"não", "{nã}"}, {"casas", "{cas}"}} {
				portugueseSmokeBoth(t, p, b, c, fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='portuguese_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'portuguese'"}})
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('portuguese','as casas e corações') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'as':1 'cas':2 'coraçõ':4 'e':3"}}))
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native stream required; PG-only discovery cannot prove replay")
			}
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_stem (stopwords=portuguese)", 0)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted SQL replay", err)
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
				dictionaryResult(t, p, b, c, q, "{}", "não")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("removed stock deterministic redump", err)
			}
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-fresh")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		defer recorded.Close()
		fresh := &fixture.Pair{Ctx: p.Ctx, Native: raw, Memory: recorded, MemoryRecord: recorder}
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fb, fc)
	})
}
