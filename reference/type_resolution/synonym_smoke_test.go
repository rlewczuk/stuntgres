package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSynonymFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		synonymCreate(t, p, b, c, "synonym_smoke")
		synonymConfig(t, p, b, c, "synonym_smoke_config", "synonym_smoke")
		constantSQL := "SELECT ts_lexize('synonym_smoke'::regdictionary,'POSTGRES'::text) AS lexemes"
		constant := synonymHeld(t, p, c, constantSQL)
		defer constant.Statement.Close()
		parameterSQL := "SELECT ts_lexize('synonym_smoke'::regdictionary,$1::text) AS lexemes"
		parameter := synonymHeld(t, p, c, parameterSQL)
		defer parameter.Statement.Close()
		dictionaryResult(t, p, b, constant, constantSQL, "{pgsql}")
		dictionaryResult(t, p, b, parameter, parameterSQL, "{pgsql}", "POSTGRES")
		values := func() {
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_smoke','POSTGRES') AS lexemes", "{pgsql}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_smoke','indices') AS lexemes", "{index}")
			synonymConverted(t, p, b, c, "SELECT to_tsquery('synonym_smoke_config','indices') AS normalized", "TSQUERY", "'index':*")
			synonymConverted(t, p, b, c, "SELECT to_tsvector('synonym_smoke_config','indices') AS normalized", "TSVECTOR", "'index':1")
			synonymConverted(t, p, b, c, "SELECT to_tsvector('synonym_smoke_config','WORD') AS normalized", "TSVECTOR", "'word':1")
		}
		values()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_smoke (casesensitive=true)", 0)
		dictionaryResult(t, p, b, constant, constantSQL, "{pgsql}")
		dictionaryResult(t, p, b, c, constantSQL, nil)
		fresh := synonymHeld(t, p, c, constantSQL)
		dictionaryResult(t, p, b, fresh, constantSQL, nil)
		fresh.Statement.Close()
		dictionaryResult(t, p, b, parameter, parameterSQL, nil, "POSTGRES")
		dictionaryResult(t, p, b, parameter, parameterSQL, "{pgsql}", "postgres")
		synonymConverted(t, p, b, c, "SELECT to_tsvector('synonym_smoke_config','POSTGRES') AS normalized", "TSVECTOR", "'postgres':1")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_smoke (casesensitive)", 0)
		values()
		before := synonymSnapshot(t, p, c, "synonym_smoke")
		synonymMissingResource(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_smoke (casesensitive=true,synonyms=synonym_missing)", "synonym_missing")
		synonymUnchanged(t, p, c, "synonym_smoke", before)
		dictionaryResult(t, p, b, constant, constantSQL, "{pgsql}")
		dictionaryResult(t, p, b, parameter, parameterSQL, "{pgsql}", "POSTGRES")
		values()
		// Controls serialize actual definitions and replay their emitted SQL;
		// neither a canned stream nor an in-memory runtime pointer is a witness.
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing native synonym smoke stream")
			}
			regtsExec(t, p, b, c, "DROP TEXT SEARCH CONFIGURATION synonym_smoke_config", 0)
			regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY synonym_smoke", 0)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual synonym smoke replay", err)
			}
			values()
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		values()
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
			t.Fatal("synonym smoke deterministic redump", err)
		}
	})
}
