package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func frenchConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.french_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "french_stem"}, {"word", "french_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "french_stem"},
		{"hword_asciipart", "french_stem"}, {"numhword", "simple"},
		{"asciihword", "french_stem"}, {"hword", "french_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSFrenchSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		frenchConfigurationMaps(t, p, b, c, "pg_catalog.french")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"aux chevaux et aux hôtels", "'cheval':2 'hôtel':5"}, {"aux et", ""}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "french", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		nullVector := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "french", nil)
		nullVector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
		run(nullVector)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, nullVector)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION french_copy (COPY=pg_catalog.french)", 0))
		frenchConfigurationMaps(t, p, b, c, "french_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION french_copy ALTER MAPPING FOR asciiword WITH simple", 0))
		var copyID, copyOwner, copyNamespace, copyParser int64
		const copySnapshot = "SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid='french_copy'::regconfig"
		if err := c.QueryRowContext(p.Ctx, copySnapshot).Scan(&copyID, &copyOwner, &copyNamespace, &copyParser); err != nil {
			t.Fatal(err)
		}
		var stem int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'french_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil {
			t.Fatal(err)
		}
		copyMaps := ftsConfigurationSimpleMaps()
		for _, row := range copyMaps {
			switch row[0].(int64) {
			case 2, 10, 11, 16, 17:
				row[2] = stem
			}
		}
		check := func() {
			run(fixture.Select(copySnapshot, []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{copyID, copyOwner, copyNamespace, copyParser}}))
			run(ftsConfigurationMaps("french_copy", copyMaps))
			run(ftsConfigurationVector("SELECT to_tsvector('french_copy','CHEVAUX hôtels') AS normalized", "'chevaux':1 'hôtel':2"))
			run(ftsConfigurationVector("SELECT to_tsvector('french','CHEVAUX hôtels') AS normalized", "'cheval':1 'hôtel':2"))
			run(fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS simple FROM pg_ts_config_map WHERE mapcfg='french_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "simple"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION french_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION french_chain (COPY=pg_catalog.french)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION french_chain ALTER MAPPING FOR word WITH simple,french_stem", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('french_chain','hôtels CHATS') AS normalized", "'chat':2 'hôtels':1"))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT mapseqno,mapdict='simple'::regdictionary::oid AS first,mapdict='french_stem'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='french_chain'::regconfig AND maptokentype=2 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('french_chain','hôtels')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "hôtels", "{simple,french_stem}", "simple", "{hôtels}"}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION french_chain ALTER MAPPING FOR asciiword WITH french_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('french_chain','CHEVAUX')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "CHEVAUX", "{french_stem,english_stem}", "french_stem", "{cheval}"}}))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY french_chain_stops (TEMPLATE=snowball,Language=french,StopWords=french)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION french_chain ALTER MAPPING FOR asciiword WITH french_chain_stops,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('french_chain','aux')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "aux", "{french_chain_stops,english_stem}", "french_chain_stops", "{}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('french_chain','aux CHATS') AS normalized", "'chat':2"))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=french", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('aux chevaux et aux hôtels') AS normalized", "'cheval':2 'hôtel':5"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSFrenchSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"asciiword", "Word, all ASCII", "CHEVAUX", "{french_stem}", "french_stem", "{cheval}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "hôtels", "{french_stem}", "french_stem", "{hôtel}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"CHEVAUX hôtels", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "french", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='french_stem'::regdictionary::oid AS same,dictionaries[1]::oid='french_stem'::regdictionary::oid AS first FROM ts_debug('french','CHEVAUX hôtels') WHERE alias <> 'blank'", []string{"same", "first"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=french", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('CHEVAUX hôtels')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
