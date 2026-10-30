// German SQL witnesses are source-reasoned [INFERENCE] pending pinned PG18.6 discovery.
package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func germanConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.german_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "german_stem"}, {"word", "german_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "german_stem"},
		{"hword_asciipart", "german_stem"}, {"numhword", "simple"},
		{"asciihword", "german_stem"}, {"hword", "german_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSGermanSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		germanConfigurationMaps(t, p, b, c, "pg_catalog.german")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"Die Häuser und Katzen", "'haus':2 'katz':4"}, {"für fuer über ueber daß dass", "'dass':6 'fur':2 'uber':4"}, {"UND", ""}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "german", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "german", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION german_copy (COPY=pg_catalog.german)", 0))
		germanConfigurationMaps(t, p, b, c, "german_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION german_copy ALTER MAPPING FOR asciiword WITH simple", 0))
		check := func() {
			run(ftsConfigurationVector("SELECT to_tsvector('german_copy','Die Häuser und Katzen') AS normalized", "'die':1 'haus':2 'katzen':4 'und':3"))
			run(ftsConfigurationVector("SELECT to_tsvector('german','Die Häuser und Katzen') AS normalized", "'haus':2 'katz':4"))
			ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('german_copy','Häuser Katzen') AS normalized", "'haus':1 'katzen':2"))
			run(fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS simple FROM pg_ts_config_map WHERE mapcfg='german_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "simple"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION german_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION german_chain (COPY=pg_catalog.german)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION german_chain ALTER MAPPING FOR asciiword WITH simple,german_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('german_chain','Die Häuser und Katzen') AS normalized", "'die':1 'haus':2 'katzen':4 'und':3"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('german_chain','KATZEN')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "KATZEN", "{simple,german_stem}", "simple", "{katzen}"}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION german_chain ALTER MAPPING FOR asciiword WITH german_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('german_chain','UND')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "UND", "{german_stem,english_stem}", "german_stem", "{}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('german_chain','UND HAUSER') AS normalized", "'haus':2"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('german_chain','GEARBEITET')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "GEARBEITET", "{german_stem,english_stem}", "german_stem", "{gearbeitet}"}}))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=german", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('Die Häuser und Katzen') AS normalized", "'haus':2 'katz':4"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSGermanSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"word", "Word, all letters", "Häuser", "{german_stem}", "german_stem", "{haus}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "und", "{german_stem}", "german_stem", "{}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"Häuser und", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "german", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='german_stem'::regdictionary::oid AS same,dictionaries[1]::oid='german_stem'::regdictionary::oid AS first FROM ts_debug('german','Häuser und') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=german", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('Häuser und')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
