package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDutchSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('dutch_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"HONDEN", "{hond}"}, {"DE", "{}"}, {"dé", "{de}"}, {"GEWERKT", "{gewerkt}"}} {
			dictionaryResult(t, p, b, c, q, tc[1], tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc[1], tc[0])
		}
		danishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('dutch',$1::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hond':2 'kat':4"}}, "De HONDEN en katten"))
		danishSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('dutch',$1::text)", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "HONDEN", "{dutch_stem}", "dutch_stem", "{hond}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "dé", "{dutch_stem}", "dutch_stem", "{de}"}}, "HONDEN dé"))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dutch_smoke (TEMPLATE=snowball,Language='DuTcH',StopWords=dutch)", 0)
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dutch_smoke'::regdictionary::oid::bigint").Scan(&id); err != nil {
			t.Fatal(err)
		}
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dutch_smoke (stopwords='',accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='dutch_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{id, "language = 'DuTcH', stopwords = 'dutch'"}})
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dutch_smoke','DE') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dutch_smoke','HONDEN') AS lexemes", "{hond}")
	})
	t.Run("removed-stock-native-stream", dutchRemovedStockControls)
}

// The PostgreSQL phase consumes the actual native bytes, never equivalent handwritten SQL.
func dutchRemovedStockControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer dutchStockCleanup(t, p, b, c)()
		q := "SELECT ts_lexize('dutch_stem',$1::text) AS lexemes"
		dictionaryResult(t, p, b, c, q, "{}", "de")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dutch_stem (stopwords)", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range [][2]string{{"de", "{de}"}, {"HONDEN", "{hond}"}} {
				danishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='dutch_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'dutch'"}})
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native stream required; PG-only discovery cannot prove replay")
			}
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dutch_stem (stopwords=dutch)", 0)
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
				dictionaryResult(t, p, b, c, q, "{}", "de")
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
