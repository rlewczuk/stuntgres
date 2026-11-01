package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func finnishConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.finnish_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "finnish_stem"}, {"word", "finnish_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "finnish_stem"},
		{"hword_asciipart", "finnish_stem"}, {"numhword", "simple"},
		{"asciihword", "finnish_stem"}, {"hword", "finnish_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSFinnishSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		finnishConfigurationMaps(t, p, b, c, "pg_catalog.finnish")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"ON TALOSSA ja talot", "'talo':2,4"}, {"ON", ""}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "finnish", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "finnish", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		control := ftsConfigurationVector(q, "'ja':3 'on':1 'talossa':2 'talot':4", "simple", "ON TALOSSA ja talot")
		run(control)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, control)
		zero := fixture.Select("SELECT to_tsvector('finnish','TALOSSA') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil)
		zero.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		ftsInspectionBoth(t, p, b, c, zero)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION finnish_copy (COPY=pg_catalog.finnish)", 0))
		finnishConfigurationMaps(t, p, b, c, "finnish_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION finnish_copy ALTER MAPPING FOR asciiword WITH simple", 0))
		var id, owner, namespace, parser, stem int64
		const snapshot = "SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid='finnish_copy'::regconfig"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &namespace, &parser); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT 'finnish_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil {
			t.Fatal(err)
		}
		expectedMaps := ftsConfigurationSimpleMaps()
		for _, row := range expectedMaps {
			switch row[0].(int64) {
			case 2, 10, 11, 16, 17:
				row[2] = stem
			}
		}
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		check := func() {
			run(fixture.Select(snapshot, []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{id, owner, namespace, parser}}))
			run(ftsConfigurationMaps("finnish_copy", expectedMaps))
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed mapping changed whole committed catalog", err)
				}
			}
			run(ftsConfigurationVector("SELECT to_tsvector('finnish_copy','TALOSSA hän talot') AS normalized", "'talossa':1 'talot':3"))
			run(ftsConfigurationVector("SELECT to_tsvector('finnish','TALOSSA hän talot') AS normalized", "'talo':1,3"))
			run(fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS english FROM pg_ts_config_map WHERE mapcfg='finnish_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "english"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION finnish_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION finnish_copy ALTER MAPPING FOR asciiword WITH finnish_stem,finnish_absent", "42704", `text search dictionary "finnish_absent" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION finnish_chain (COPY=pg_catalog.finnish)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION finnish_chain ALTER MAPPING FOR asciiword WITH simple,finnish_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('finnish_chain','TALOSSA hän talot') AS normalized", "'talossa':1 'talot':3"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('finnish_chain','TALOSSA')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "TALOSSA", "{simple,finnish_stem}", "simple", "{talossa}"}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION finnish_chain ALTER MAPPING FOR asciiword WITH finnish_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('finnish_chain','ON')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "ON", "{finnish_stem,english_stem}", "finnish_stem", "{}"}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('finnish_chain','RATS')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{finnish_stem,english_stem}", "finnish_stem", "{rats}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('finnish_chain','ON TALOSSA') AS normalized", "'talo':2"))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=finnish", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('ON TALOSSA ja talot') AS normalized", "'talo':2,4"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSFinnishSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"asciiword", "Word, all ASCII", "TALOSSA", "{finnish_stem}", "finnish_stem", "{talo}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "hän", "{finnish_stem}", "finnish_stem", "{}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"TALOSSA hän", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "finnish", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		zero := fixture.Select("SELECT * FROM ts_debug('finnish','TALOSSA hän') WHERE false", ftsDebugColumns, ftsDebugTypes, nil)
		zero.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
		ftsInspectionBoth(t, p, b, c, zero)
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='finnish_stem'::regdictionary::oid AS same,dictionaries[1]::oid='finnish_stem'::regdictionary::oid AS first FROM ts_debug('finnish','TALOSSA hän') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=finnish", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('TALOSSA hän')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
