package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func catalanConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.catalan_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
		t.Fatal(stem, err)
	}
	rows := ftsConfigurationSimpleMaps()
	for _, row := range rows {
		switch row[0].(int64) {
		case 1, 2, 10, 11, 16, 17:
			row[2] = stem
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, ftsConfigurationMaps(name, rows))
	// Specify aliases independently of numeric parser ordering, then join the
	// actual parser token declarations to the actual configuration mappings.
	aliases := [][]any{
		{"asciiword", "catalan_stem"}, {"word", "catalan_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "catalan_stem"},
		{"hword_asciipart", "catalan_stem"}, {"numhword", "simple"},
		{"asciihword", "catalan_stem"}, {"hword", "catalan_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSCatalanSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		catalanConfigurationMaps(t, p, b, c, "pg_catalog.catalan")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"GATS cases i café", "'caf':4 'case':2 'gat':1 'i':3"}, {"ONLY", "'only':1"}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "catalan", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "catalan", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION catalan_copy (COPY=pg_catalog.catalan)", 0))
		catalanConfigurationMaps(t, p, b, c, "catalan_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION catalan_copy ALTER MAPPING FOR asciiword WITH simple", 0))
		check := func() {
			run(ftsConfigurationVector("SELECT to_tsvector('catalan_copy','GATS café') AS normalized", "'caf':2 'gats':1"))
			run(ftsConfigurationVector("SELECT to_tsvector('catalan','GATS café') AS normalized", "'caf':2 'gat':1"))
			run(fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS english FROM pg_ts_config_map WHERE mapcfg='catalan_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "english"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION catalan_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION catalan_chain (COPY=pg_catalog.catalan)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION catalan_chain ALTER MAPPING FOR word WITH simple,catalan_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('catalan_chain','GATS café') AS normalized", "'café':2 'gat':1"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS first,mapdict='catalan_stem'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='catalan_chain'::regconfig AND maptokentype=2 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('catalan_chain','café')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "café", "{simple,catalan_stem}", "simple", "{café}"}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION catalan_chain ALTER MAPPING FOR asciiword WITH catalan_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('catalan_chain','GATS')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "GATS", "{catalan_stem,english_stem}", "catalan_stem", "{gat}"}}))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY catalan_chain_stops (TEMPLATE=snowball,Language=catalan,StopWords=english)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION catalan_chain ALTER MAPPING FOR asciiword WITH catalan_chain_stops,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('catalan_chain','ONLY')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "ONLY", "{catalan_chain_stops,english_stem}", "catalan_chain_stops", "{}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('catalan_chain','ONLY GATS') AS normalized", "'gat':2"))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=catalan", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('GATS cases i café') AS normalized", "'caf':4 'case':2 'gat':1 'i':3"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSCatalanSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"asciiword", "Word, all ASCII", "GATS", "{catalan_stem}", "catalan_stem", "{gat}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "café", "{catalan_stem}", "catalan_stem", "{caf}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"GATS café", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "catalan", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='catalan_stem'::regdictionary::oid AS same,dictionaries[1]::oid='catalan_stem'::regdictionary::oid AS first FROM ts_debug('catalan','GATS café') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=catalan", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('GATS café')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
