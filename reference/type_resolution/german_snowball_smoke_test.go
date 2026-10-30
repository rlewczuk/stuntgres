package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// SQL witnesses are independently source-reasoned, pending pinned PG18.6 discovery.
func TestReferenceGermanSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TEXT SEARCH DICTIONARY german_smoke (TEMPLATE=snowball,Language='GeRmAn')",
			"CREATE TEXT SEARCH CONFIGURATION german_smoke_config (COPY=german)",
			"CREATE TABLE german_smoke_rows(d regdictionary,c regconfig)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO german_smoke_rows VALUES('german_smoke','german_smoke_config')", 1)
		check := func() {
			q := "SELECT ts_lexize('german_stem',$1::text) AS lexemes"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range [][2]string{{"HäUSER", "{haus}"}, {"HÄUSER", "{hÄus}"}, {"und", "{}"}, {"gearbeitet", "{gearbeitet}"}} {
				for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
					dictionaryResult(t, p, b, e, q, tc[1], tc[0])
				}
			}
			dictionaryResult(t, p, b, c, "SELECT ts_lexize(d,'und') AS lexemes FROM german_smoke_rows", "{und}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize(d,'HäUSER') AS lexemes FROM german_smoke_rows", "{haus}")
			for _, q := range []string{"SELECT to_tsvector('german',$1::text) AS normalized", "SELECT to_tsvector(c,$1::text) AS normalized FROM german_smoke_rows"} {
				danishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'haus':2 'katz':4"}}, "Die Häuser und Katzen"))
			}
			danishSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('german',$1::text)", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "Häuser", "{german_stem}", "german_stem", "{haus}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "und", "{german_stem}", "german_stem", "{}"}}, "Häuser und"))
			regtsSelect(t, p, b, c, "SELECT d='german_smoke'::regdictionary AS dictionary,c='german_smoke_config'::regconfig AS configuration FROM german_smoke_rows", []string{"dictionary", "configuration"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		}
		check()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native stream required for replay")
			}
			for _, q := range []string{"DROP TABLE german_smoke_rows", "DROP TEXT SEARCH CONFIGURATION german_smoke_config", "DROP TEXT SEARCH DICTIONARY german_smoke"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native SQL replay", err)
			}
			check()
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check()
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
			t.Fatal("smoke deterministic redump", err)
		}
	})
}
