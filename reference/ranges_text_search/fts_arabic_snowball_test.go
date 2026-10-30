package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func arabicConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.arabic_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "arabic_stem"}, {"word", "arabic_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "arabic_stem"},
		{"hword_asciipart", "arabic_stem"}, {"numhword", "simple"},
		{"asciihword", "arabic_stem"}, {"hword", "arabic_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSArabicSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		arabicConfigurationMaps(t, p, b, c, "pg_catalog.arabic")
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"الكتاب كتابه RATS", "'rats':3 'كتاب':1,2"}, {"ONLY", "'only':1"}, {"", ""}} {
			s := ftsConfigurationVector(q, tc[1], "arabic", tc[0])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION arabic_copy (COPY=pg_catalog.arabic)", 0))
		arabicConfigurationMaps(t, p, b, c, "arabic_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION arabic_copy ALTER MAPPING FOR asciiword WITH english_stem", 0))
		check := func() {
			run(ftsConfigurationVector("SELECT to_tsvector('arabic_copy','الكتاب RATS') AS normalized", "'rat':2 'كتاب':1"))
			run(ftsConfigurationVector("SELECT to_tsvector('arabic','الكتاب RATS') AS normalized", "'rats':2 'كتاب':1"))
			run(fixture.Select("SELECT mapseqno,mapdict='english_stem'::regdictionary::oid AS english FROM pg_ts_config_map WHERE mapcfg='arabic_copy'::regconfig AND maptokentype=1", []string{"mapseqno", "english"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		}
		check()
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION arabic_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			check()
		}
		var prior string
		if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET default_text_search_config=arabic", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('الكتاب كتابه RATS') AS normalized", "'rats':3 'كتاب':1,2"))
		run(fixture.Exec("RESET default_text_search_config", 0))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{prior}}))
	})
}

func TestReferenceFTSArabicSnowballDebug(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := [][]any{{"word", "Word, all letters", "الكتاب", "{arabic_stem}", "arabic_stem", "{كتاب}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "RATS", "{arabic_stem}", "arabic_stem", "{rats}"}}
		q := "SELECT * FROM ts_debug($1::regconfig,$2::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			document any
			rows     [][]any
		}{{"الكتاب RATS", want}, {"", nil}, {nil, nil}} {
			s := fixture.Select(q, ftsDebugColumns, ftsDebugTypes, tc.rows, "arabic", tc.document)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsInspectionMetadata(t, b, r, false) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config=arabic", 0))
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('الكتاب RATS')", ftsDebugColumns, ftsDebugTypes, want))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET default_text_search_config", 0))
	})
}
