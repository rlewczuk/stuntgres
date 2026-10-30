package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func armenianConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.armenian_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "armenian_stem"}, {"word", "armenian_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "armenian_stem"},
		{"hword_asciipart", "armenian_stem"}, {"numhword", "simple"},
		{"asciihword", "armenian_stem"}, {"hword", "armenian_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSArmenianSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		armenianConfigurationMaps(t, p, b, c, "pg_catalog.armenian")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"աղոթքը աղոթքներ RATS", "'rats':3 'աղոթ':1,2"}, {"ONLY", "'only':1"}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "armenian", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "armenian", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION armenian_copy (COPY=pg_catalog.armenian)", 0))
		armenianConfigurationMaps(t, p, b, c, "armenian_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION armenian_copy ALTER MAPPING FOR asciiword WITH english_stem", 0))
		check := func() {
			run(ftsConfigurationVector("SELECT to_tsvector('armenian_copy','աղոթքը RATS') AS normalized", "'rat':2 'աղոթ':1"))
			run(ftsConfigurationVector("SELECT to_tsvector('armenian','աղոթքը RATS') AS normalized", "'rats':2 'աղոթ':1"))
			run(fixture.Select("SELECT mapseqno,mapdict='english_stem'::regdictionary::oid AS english FROM pg_ts_config_map WHERE mapcfg='armenian_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "english"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION armenian_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION armenian_chain (COPY=pg_catalog.armenian)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION armenian_chain ALTER MAPPING FOR word WITH simple,armenian_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('armenian_chain','աղոթքը RATS') AS normalized", "'rats':2 'աղոթքը':1"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS first,mapdict='armenian_stem'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='armenian_chain'::regconfig AND maptokentype=2 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('armenian_chain','աղոթքը')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "աղոթքը", "{simple,armenian_stem}", "simple", "{աղոթքը}"}}))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=armenian", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('աղոթքը աղոթքներ RATS') AS normalized", "'rats':3 'աղոթ':1,2"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSArmenianSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"word", "Word, all letters", "աղոթքը", "{armenian_stem}", "armenian_stem", "{աղոթ}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "RATS", "{armenian_stem}", "armenian_stem", "{rats}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"աղոթքը RATS", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "armenian", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='armenian_stem'::regdictionary::oid AS same,dictionaries[1]::oid='armenian_stem'::regdictionary::oid AS first FROM ts_debug('armenian','աղոթքը RATS') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=armenian", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('աղոթքը RATS')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
