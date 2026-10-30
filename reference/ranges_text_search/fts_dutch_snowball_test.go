package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func dutchConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.dutch_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "dutch_stem"}, {"word", "dutch_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "dutch_stem"},
		{"hword_asciipart", "dutch_stem"}, {"numhword", "simple"},
		{"asciihword", "dutch_stem"}, {"hword", "dutch_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSDutchSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		dutchConfigurationMaps(t, p, b, c, "pg_catalog.dutch")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"De HONDEN en katten", "'hond':2 'kat':4"}, {"dé de", "'de':1"}, {"DE", ""}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "dutch", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "dutch", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION dutch_copy (COPY=pg_catalog.dutch)", 0))
		dutchConfigurationMaps(t, p, b, c, "dutch_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION dutch_copy ALTER MAPPING FOR asciiword WITH simple", 0))
		check := func() {
			run(ftsConfigurationVector("SELECT to_tsvector('dutch_copy','De HONDEN en katten') AS normalized", "'de':1 'en':3 'honden':2 'katten':4"))
			run(ftsConfigurationVector("SELECT to_tsvector('dutch','De HONDEN en katten') AS normalized", "'hond':2 'kat':4"))
			ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('dutch_copy','dé de') AS normalized", "'de':1,2"))
			run(fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS simple FROM pg_ts_config_map WHERE mapcfg='dutch_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "simple"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION dutch_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION dutch_chain (COPY=pg_catalog.dutch)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION dutch_chain ALTER MAPPING FOR asciiword WITH simple,dutch_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('dutch_chain','De HONDEN en katten') AS normalized", "'de':1 'en':3 'honden':2 'katten':4"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('dutch_chain','HONDEN')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "HONDEN", "{simple,dutch_stem}", "simple", "{honden}"}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION dutch_chain ALTER MAPPING FOR asciiword WITH dutch_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('dutch_chain','DE')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "DE", "{dutch_stem,english_stem}", "dutch_stem", "{}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('dutch_chain','DE HONDEN') AS normalized", "'hond':2"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('dutch_chain','GEWERKT')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "GEWERKT", "{dutch_stem,english_stem}", "dutch_stem", "{gewerkt}"}}))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=dutch", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('De HONDEN en katten') AS normalized", "'hond':2 'kat':4"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSDutchSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"asciiword", "Word, all ASCII", "HONDEN", "{dutch_stem}", "dutch_stem", "{hond}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "dé", "{dutch_stem}", "dutch_stem", "{de}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"HONDEN dé", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "dutch", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='dutch_stem'::regdictionary::oid AS same,dictionaries[1]::oid='dutch_stem'::regdictionary::oid AS first FROM ts_debug('dutch','HONDEN dé') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=dutch", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('HONDEN dé')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
