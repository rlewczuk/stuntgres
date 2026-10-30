package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hindiConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var hindi, english int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.hindi_stem'::regdictionary::oid::bigint,'pg_catalog.english_stem'::regdictionary::oid::bigint").Scan(&hindi, &english); err != nil || hindi <= 0 || english <= 0 || hindi == english {
		t.Fatal("distinct actual dictionaries", hindi, english, err)
	}
	rows := [][]any{
		{int64(1), int64(1), english}, {int64(2), int64(1), hindi},
		{int64(3), int64(1), int64(3765)}, {int64(4), int64(1), int64(3765)},
		{int64(5), int64(1), int64(3765)}, {int64(6), int64(1), int64(3765)},
		{int64(7), int64(1), int64(3765)}, {int64(8), int64(1), int64(3765)},
		{int64(9), int64(1), int64(3765)}, {int64(10), int64(1), hindi},
		{int64(11), int64(1), english}, {int64(15), int64(1), int64(3765)},
		{int64(16), int64(1), english}, {int64(17), int64(1), hindi},
		{int64(18), int64(1), int64(3765)}, {int64(19), int64(1), int64(3765)},
		{int64(20), int64(1), int64(3765)}, {int64(21), int64(1), int64(3765)},
		{int64(22), int64(1), int64(3765)},
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, ftsConfigurationMaps(name, rows))
	aliases := [][]any{
		{"asciiword", "english_stem"}, {"word", "hindi_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "hindi_stem"},
		{"hword_asciipart", "english_stem"}, {"numhword", "simple"},
		{"asciihword", "english_stem"}, {"hword", "hindi_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSHindiSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		hindiConfigurationMaps(t, p, b, c, "pg_catalog.hindi")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			input any
			want  any
		}{
			{"THE RATS कमल", "'rat':2 'कमल':3"}, {"ONLY", ""}, {"", ""}, {nil, nil},
		} {
			s := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, "hindi", tc.input)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { configurationVectorMetadata(t, rows, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION hindi_copy (COPY=pg_catalog.hindi)", 0))
		hindiConfigurationMaps(t, p, b, c, "hindi_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION hindi_copy ALTER MAPPING FOR asciiword WITH hindi_stem", 0))
		check := func() {
			ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('hindi_copy','THE RATS कमल') AS normalized", "'rats':2 'the':1 'कमल':3"))
			ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('hindi','THE RATS कमल') AS normalized", "'rat':2 'कमल':3"))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION hindi_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		// A mark-free word plus independent dictionary lexization witnesses the
		// remap without presuming how the parser splits Devanagari vowel signs.
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION hindi_copy ALTER MAPPING FOR word WITH simple", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('hindi_copy','कमल')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "कमल", "{simple}", "simple", "{कमल}"}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hindi_stem','करती') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{कर}"}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('hindi_copy','कमलअ')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "कमलअ", "{simple}", "simple", "{कमलअ}"}}))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('hindi','कमलअ')", ftsDebugColumns, ftsDebugTypes, [][]any{{"word", "Word, all letters", "कमलअ", "{hindi_stem}", "hindi_stem", "{कमल}"}}))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION hindi_chain (COPY=hindi)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION hindi_chain ALTER MAPPING FOR asciiword WITH hindi_stem,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('hindi_chain','RATS')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{hindi_stem,english_stem}", "hindi_stem", "{rats}"}}))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY hindi_chain_stops (TEMPLATE=snowball,Language=hindi,StopWords=english)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION hindi_chain ALTER MAPPING FOR asciiword WITH hindi_chain_stops,english_stem", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('hindi_chain','ONLY')", ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "ONLY", "{hindi_chain_stops,english_stem}", "hindi_chain_stops", "{}"}}))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('hindi_chain','ONLY RATS') AS normalized", "'rats':2"))
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=hindi", 0))
		ftsInspectionBoth(t, p, b, c, ftsConfigurationVector("SELECT to_tsvector('THE RATS कमल') AS normalized", "'rat':2 'कमल':3"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSHindiSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"asciiword", "Word, all ASCII", "RATS", "{english_stem}", "english_stem", "{rat}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "कमल", "{hindi_stem}", "hindi_stem", "{कमल}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			input any
			rows  [][]any
		}{{"RATS कमल", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "hindi", tc.input)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ftsInspectionMetadata(t, b, rows, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT dictionary::oid='english_stem'::regdictionary::oid AS english,dictionaries[1]::oid='english_stem'::regdictionary::oid AS first_english,dictionary::oid='hindi_stem'::regdictionary::oid AS hindi,dictionaries[1]::oid='hindi_stem'::regdictionary::oid AS first_hindi FROM ts_debug('hindi','RATS कमल') WHERE alias <> 'blank'", []string{"english", "first_english", "hindi", "first_hindi"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false, false}, {false, false, true, true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=hindi", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('RATS कमल')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
