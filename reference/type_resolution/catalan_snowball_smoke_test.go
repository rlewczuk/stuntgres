package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func catalanMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "catalan.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "catalan.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func catalanSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
	}
}

func TestReferenceCatalanSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stock := "SELECT ts_lexize('catalan_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, stock)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"GATS", "gat"}, {"café", "caf"}, {"ONLY", "only"}, {"basela", "basel"}, {"bìbas", "bibas"}} {
			dictionaryResult(t, p, b, c, stock, "{"+tc[1]+"}", tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: stock}, stock, "{"+tc[1]+"}", tc[0])
		}
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='catalan_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'catalan'"}})
		maps := [][]any{}
		for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
			stem := id == 1 || id == 2 || id == 10 || id == 11 || id == 16 || id == 17
			maps = append(maps, []any{id, int64(1), stem, !stem})
		}
		regtsSelect(t, p, b, c, "SELECT maptokentype,mapseqno,mapdict='catalan_stem'::regdictionary::oid AS stem,mapdict='simple'::regdictionary::oid AS simple FROM pg_ts_config_map WHERE mapcfg='catalan'::regconfig ORDER BY maptokentype", []string{"maptokentype", "mapseqno", "stem", "simple"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, maps)
		catalanSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('catalan','GATS cases i café') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'caf':4 'case':2 'gat':1 'i':3"}}))
		catalanSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('catalan','GATS café')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "GATS", "{catalan_stem}", "catalan_stem", "{gat}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "café", "{catalan_stem}", "catalan_stem", "{caf}"}}))
		for _, q := range []string{
			"CREATE TEXT SEARCH DICTIONARY catalan_smoke (TEMPLATE=snowball,Language='CaTaLaN',StopWords=english)",
			"CREATE TEXT SEARCH CONFIGURATION catalan_smoke_config (COPY=catalan)",
			"ALTER TEXT SEARCH CONFIGURATION catalan_smoke_config ALTER MAPPING FOR asciiword WITH catalan_smoke",
			"CREATE TABLE catalan_smoke_refs(d regdictionary DEFAULT 'catalan_smoke'::regdictionary,c regconfig DEFAULT 'catalan_smoke_config'::regconfig)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO catalan_smoke_refs DEFAULT VALUES", 1)
		constantSQL := "SELECT ts_lexize('catalan_smoke','ONLY') AS lexemes"
		parameterSQL := "SELECT ts_lexize('catalan_smoke',$1::text) AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constant.Close()
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		ch := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		ph := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{}", "ONLY")
		dictionaryResult(t, p, b, ph, parameterSQL, "{gat}", "GATS")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY catalan_smoke (stopwords)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "ONLY")
		dictionaryResult(t, p, b, c, constantSQL, "{only}")
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'catalan_smoke'::regdictionary::oid::bigint").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		catalanMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY catalan_smoke (language=catalan,stopwords=catalan)")
		regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='catalan_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{identity, "language = 'CaTaLaN'"}})
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		check := func() {
			dictionaryResult(t, p, b, c, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, parameterSQL, "{gat}", "GATS")
			regtsSelect(t, p, b, c, "SELECT d='catalan_smoke'::regdictionary AS d,c='catalan_smoke_config'::regconfig AS c FROM catalan_smoke_refs", []string{"d", "c"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('catalan_smoke_config','GATS ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gat':1 'only':2"}})
		}
		check()
		stmt.Close()
		constant.Close()
		parameter.Close()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted SQL required")
			}
			for _, q := range []string{"DROP TABLE catalan_smoke_refs", "DROP TEXT SEARCH CONFIGURATION catalan_smoke_config", "DROP TEXT SEARCH DICTIONARY catalan_smoke"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native replay", err)
			}
			check()
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
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='catalan_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'catalan'"}})
		dictionaryResult(t, p, b, c, stock, "{only}", "ONLY")
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check()
	})
}
