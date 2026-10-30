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

func estonianMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "estonian.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "estonian.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func estonianSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
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

func TestReferenceEstonianSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.estonian_stem (language=estonian,stopwords)"); err != nil {
					t.Error("restore stock Estonian", err)
				}
			}()
		}
		stock := "SELECT ts_lexize('estonian_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, stock)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"RAAMATUD", "raama"}, {"viin", "viima"}, {"ONLY", "only"}, {"RATS", "rats"}} {
			dictionaryResult(t, p, b, c, stock, "{"+tc[1]+"}", tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: stock}, stock, "{"+tc[1]+"}", tc[0])
		}
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY estonian_smoke (TEMPLATE=snowball,Language='EsToNiAn',StopWords=english)", "CREATE TEXT SEARCH CONFIGURATION estonian_smoke_config (COPY=estonian)", "CREATE TABLE estonian_smoke_refs(d regdictionary DEFAULT 'estonian_smoke'::regdictionary)"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO estonian_smoke_refs DEFAULT VALUES", 1)
		for _, tc := range [][2]string{{"ONLY", "{}"}, {"RATS", "{rats}"}, {"RAAMATUD", "{raama}"}} {
			estonianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('estonian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		estonianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('estonian','ja raamatud mõttele RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ja':1 'mõtt':3 'raama':2 'rats':4"}}))
		estonianSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('estonian','RAAMATUD mõttele')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "RAAMATUD", "{estonian_stem}", "estonian_stem", "{raama}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "mõttele", "{estonian_stem}", "estonian_stem", "{mõtt}"}}))
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'estonian_smoke'::regdictionary::oid::bigint").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_smoke (language=english,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='estonian_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{identity, "language = 'EsToNiAn', stopwords = 'english'"}})
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('estonian_smoke','RATS') AS lexemes", "{rats}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.estonian_stem (stopwords=english)", 0)
		dictionaryResult(t, p, b, c, stock, "{}", "ONLY")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.estonian_stem (stopwords,language='EsToNiAn')", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			t.Helper()
			dictionaryResult(t, p, b, c, stock, "{only}", "ONLY")
			dictionaryResult(t, p, b, c, stock, "{raama}", "RAAMATUD")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('estonian_smoke','ONLY') AS lexemes", "{}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('estonian_smoke','RATS') AS lexemes", "{rats}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='estonian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'EsToNiAn'"}})
			regtsSelect(t, p, b, c, "SELECT d='estonian_smoke'::regdictionary AS same FROM estonian_smoke_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
		}
		check(p, b, c)
		stmt.Close()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native emitted SQL required")
			}
			for _, q := range []string{"DROP TABLE estonian_smoke_refs", "DROP TEXT SEARCH CONFIGURATION estonian_smoke_config", "DROP TEXT SEARCH DICTIONARY estonian_smoke", "ALTER TEXT SEARCH DICTIONARY pg_catalog.estonian_stem (language=estonian,stopwords)"} {
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
				dictionaryResult(t, p, b, c, stock, "{only}", "ONLY")
				regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='estonian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'estonian'"}})
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
