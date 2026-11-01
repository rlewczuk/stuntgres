package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFinnishSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('finnish_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"TALOSSA", "{talo}"}, {"ON", "{}"}, {"HÄN", "{hÄn}"}, {"babxhen", "{babxhen}"}, {"talohun", "{talohu}"}, {"talo'", "{talo'}"}} {
			dictionaryResult(t, p, b, c, q, tc[1], tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc[1], tc[0])
		}
		finnishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('finnish',$1::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'talo':2,4"}}, "ON TALOSSA ja talot"))
		finnishSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('finnish',$1::text)", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "TALOSSA", "{finnish_stem}", "finnish_stem", "{talo}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "hän", "{finnish_stem}", "finnish_stem", "{}"}}, "TALOSSA hän"))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY finnish_smoke (TEMPLATE=snowball,Language='FiNnIsH',StopWords=finnish)", 0)
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'finnish_smoke'::regdictionary::oid::bigint").Scan(&id); err != nil {
			t.Fatal(err)
		}
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_smoke (stopwords='',accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='finnish_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{id, "language = 'FiNnIsH', stopwords = 'finnish'"}})
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('finnish_smoke','ON') AS lexemes", "{}")
	})
	t.Run("removed-stock-native-stream", finnishRemovedStockControls)
}

// The PostgreSQL phase consumes the actual native bytes, never equivalent handwritten SQL.
func finnishRemovedStockControls(t *testing.T) {
	t.Run("absent", func(t *testing.T) { finnishStockOptionControls(t, false) })
	t.Run("explicit-empty", func(t *testing.T) { finnishStockOptionControls(t, true) })
}

func finnishStockOptionControls(t *testing.T, empty bool) {
	t.Helper()
	option, metadata := "stopwords", "language = 'finnish'"
	if empty {
		option, metadata = "stopwords=''", "language = 'finnish', stopwords = ''"
	}
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer finnishStockCleanup(t, p, b, c)()
		q := "SELECT ts_lexize('finnish_stem',$1::text) AS lexemes"
		dictionaryResult(t, p, b, c, q, "{}", "on")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_stem ("+option+")", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range [][2]string{{"on", "{on}"}, {"TALOSSA", "{talo}"}} {
				finnishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='finnish_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{metadata}})
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native stream required; PG-only discovery cannot prove replay")
			}
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_stem (stopwords=finnish)", 0)
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
				dictionaryResult(t, p, b, c, q, "{}", "on")
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
		fresh := fixture.NewFixture(t, false, true)
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
