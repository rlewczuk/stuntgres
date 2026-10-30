package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func greekMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "greek.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "greek.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceGreekSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stock := "SELECT ts_lexize('greek_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, stock)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"ΚΑΛΌΣ", "{καλ}"}, {"RATS", "{rats}"}, {"ONLY", "{only}"}} {
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: stock}} {
				dictionaryResult(t, p, b, e, stock, tc[1], tc[0])
			}
		}
		regtsSelect(t, p, b, c, "SELECT oid::bigint>0 AS allocated,dictinitoption FROM pg_ts_dict WHERE oid='greek_stem'::regdictionary", []string{"allocated", "dictinitoption"}, []string{"BOOL", "TEXT"}, [][]any{{true, "language = 'greek'"}})
		for _, s := range []fixture.Step{
			fixture.Select("SELECT to_tsvector('greek','THE καλός κρέατα') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'the':1 'καλ':2 'κρε':3"}}),
			fixture.Select("SELECT * FROM ts_debug('greek','RATS καλός')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{greek_stem}", "greek_stem", "{rats}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "καλός", "{greek_stem}", "greek_stem", "{καλ}"}}),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			held, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: s.Query}, b.Recorder, s)
			held.Close()
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY greek_smoke (TEMPLATE=snowball,Language=greek,StopWords=english)", 0)
		q := "SELECT ts_lexize('greek_smoke',$1::text) AS lexemes"
		live, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer live.Close()
		held := fixture.HeldStatement{Executor: c, Statement: live, SQL: q}
		dictionaryResult(t, p, b, held, q, "{}", "ONLY")
		dictionaryResult(t, p, b, held, q, "{καλ}", "καλός")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_smoke (stopwords)", 0)
		dictionaryResult(t, p, b, held, q, "{only}", "ONLY")
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'greek_smoke'::regdictionary::oid::bigint").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		greekMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_smoke (language=english,stopwords=greek)")
		check := func() {
			regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='greek_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{identity, "language = 'greek'"}})
			for _, e := range []fixture.Executor{c, held} {
				for _, tc := range [][2]string{{"ONLY", "{only}"}, {"RATS", "{rats}"}, {"καλός", "{καλ}"}} {
					dictionaryResult(t, p, b, e, q, tc[1], tc[0])
				}
			}
		}
		check()
		if b.PG {
			return
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed ALTER changed whole Dump", err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		dictionaryResult(t, p, b, c, stock, "{only}", "ONLY")
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(before.Bytes(), again.Bytes()) {
			t.Fatal("smoke redump", err)
		}
	})
}
