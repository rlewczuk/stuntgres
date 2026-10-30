package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func greekConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.greek_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "greek_stem"}, {"word", "greek_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "greek_stem"},
		{"hword_asciipart", "greek_stem"}, {"numhword", "simple"},
		{"asciihword", "greek_stem"}, {"hword", "greek_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSGreekSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		greekConfigurationMaps(t, p, b, c, "pg_catalog.greek")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"THE καλός κρέατα", "'the':1 'καλ':2 'κρε':3"}, {"αλαικο γαανε αβαραετε", "'αβαρετετ':3 'αλικικ':1 'γαναν':2"}, {"ONLY", "'only':1"}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "greek", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "greek", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION greek_copy (COPY=pg_catalog.greek)", 0))
		greekConfigurationMaps(t, p, b, c, "greek_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION greek_copy ALTER MAPPING FOR word WITH simple", 0))
		check := func() {
			run(ftsConfigurationVector("SELECT to_tsvector('greek_copy','καλός RATS') AS normalized", "'rats':2 'καλός':1"))
			run(ftsConfigurationVector("SELECT to_tsvector('greek','καλός RATS') AS normalized", "'rats':2 'καλ':1"))
			run(fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS simple FROM pg_ts_config_map WHERE mapcfg='greek_copy'::regconfig AND maptokentype=2", []string{"mapseqno", "simple"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION greek_copy ALTER MAPPING FOR word,not_a_token WITH greek_stem", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION greek_chain (COPY=pg_catalog.greek)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION greek_chain ALTER MAPPING FOR word WITH simple,greek_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('greek_chain','καλός RATS') AS normalized", "'rats':2 'καλός':1"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS first,mapdict='greek_stem'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='greek_chain'::regconfig AND maptokentype=2 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('greek_chain','καλός')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "καλός", "{simple,greek_stem}", "simple", "{καλός}"}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION greek_chain ALTER MAPPING FOR asciiword WITH greek_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('greek_chain','RATS')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{greek_stem,english_stem}", "greek_stem", "{rats}"}}))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY greek_chain_stops (TEMPLATE=snowball,Language=greek,StopWords=english)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION greek_chain ALTER MAPPING FOR asciiword WITH greek_chain_stops,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('greek_chain','ONLY')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "ONLY", "{greek_chain_stops,english_stem}", "greek_chain_stops", "{}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('greek_chain','ONLY RATS') AS normalized", "'rats':2"))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=greek", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('THE καλός κρέατα') AS normalized", "'the':1 'καλ':2 'κρε':3"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSGreekSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"asciiword", "Word, all ASCII", "RATS", "{greek_stem}", "greek_stem", "{rats}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "καλός", "{greek_stem}", "greek_stem", "{καλ}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"RATS καλός", want}, {"αλαικο γαανε αβαραετε", [][]any{
			{"word", "Word, all letters", "αλαικο", "{greek_stem}", "greek_stem", "{αλικικ}"},
			{"blank", "Space symbols", " ", "{}", nil, nil},
			{"word", "Word, all letters", "γαανε", "{greek_stem}", "greek_stem", "{γαναν}"},
			{"blank", "Space symbols", " ", "{}", nil, nil},
			{"word", "Word, all letters", "αβαραετε", "{greek_stem}", "greek_stem", "{αβαρετετ}"},
		}}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "greek", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='greek_stem'::regdictionary::oid AS same,dictionaries[1]::oid='greek_stem'::regdictionary::oid AS first FROM ts_debug('greek','RATS καλός') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=greek", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('RATS καλός')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
