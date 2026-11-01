package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"path"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func frenchMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "french_missing.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "french_missing.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func frenchSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}
	for range 2 {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceFrenchSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (language=french,stopwords=french)"); err != nil {
					t.Error("restore stock French", err)
				}
			}()
		}
		stock := "SELECT ts_lexize('french_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, stock)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"CHEVAUX", "cheval"}, {"hôtels", "hôtel"}, {"les", "le"}, {"bijoux", "bijoux"}, {"français", "franc"}, {"l'été", "été"}} {
			dictionaryResult(t, p, b, c, stock, "{"+tc[1]+"}", tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: stock}, stock, "{"+tc[1]+"}", tc[0])
		}
		for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: stock}} {
			dictionaryResult(t, p, b, e, stock, "{}", "été")
		}
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY french_smoke (TEMPLATE=snowball,Language='FrEnCh',StopWords=french)", "CREATE TEXT SEARCH CONFIGURATION french_smoke_config (COPY=french)", "CREATE TABLE french_smoke_refs(d regdictionary DEFAULT 'french_smoke'::regdictionary)"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO french_smoke_refs DEFAULT VALUES", 1)
		for _, tc := range [][2]string{{"été", "{}"}, {"CHATS", "{chat}"}, {"CHEVAUX", "{cheval}"}} {
			frenchSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('french_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		frenchSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('french','aux chevaux et aux hôtels') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cheval':2 'hôtel':5"}}))
		frenchSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('french','CHEVAUX hôtels')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "CHEVAUX", "{french_stem}", "french_stem", "{cheval}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "hôtels", "{french_stem}", "french_stem", "{hôtel}"}}))
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'french_smoke'::regdictionary::oid::bigint").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_smoke (language=english,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='french_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{identity, "language = 'FrEnCh', stopwords = 'french'"}})
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('french_smoke','CHATS') AS lexemes", "{chat}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (stopwords=french)", 0)
		dictionaryResult(t, p, b, c, stock, "{}", "été")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (stopwords,language='FrEnCh')", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			t.Helper()
			dictionaryResult(t, p, b, c, stock, "{été}", "été")
			dictionaryResult(t, p, b, c, stock, "{cheval}", "CHEVAUX")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('french_smoke','été') AS lexemes", "{}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('french_smoke','CHATS') AS lexemes", "{chat}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='french_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'FrEnCh'"}})
			regtsSelect(t, p, b, c, "SELECT d='french_smoke'::regdictionary AS same FROM french_smoke_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
		}
		check(p, b, c)
		stmt.Close()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native emitted SQL required")
			}
			for _, q := range []string{"DROP TABLE french_smoke_refs", "DROP TEXT SEARCH CONFIGURATION french_smoke_config", "DROP TEXT SEARCH DICTIONARY french_smoke", "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (language=french,stopwords=french)"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native SQL replay", err)
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
				dictionaryResult(t, p, b, c, stock, "{}", "été")
				dictionaryResult(t, p, b, c, stock, "{été}", "l'été")
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='french'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
				regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='french_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'french', stopwords = 'french'"}})
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("deterministic smoke redump", err)
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
