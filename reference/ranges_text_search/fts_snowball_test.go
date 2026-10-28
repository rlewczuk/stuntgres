package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func snowballConfigurationMaps(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	var stem int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.english_stem'::regdictionary::oid::bigint").Scan(&stem); err != nil || stem <= 0 {
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
		{"asciiword", "english_stem"}, {"word", "english_stem"},
		{"numword", "simple"}, {"email", "simple"}, {"url", "simple"},
		{"host", "simple"}, {"sfloat", "simple"}, {"version", "simple"},
		{"hword_numpart", "simple"}, {"hword_part", "english_stem"},
		{"hword_asciipart", "english_stem"}, {"numhword", "simple"},
		{"asciihword", "english_stem"}, {"hword", "english_stem"},
		{"url_path", "simple"}, {"file", "simple"}, {"float", "simple"},
		{"int", "simple"}, {"uint", "simple"},
	}
	s := fixture.Select("SELECT t.alias,d.dictname FROM ts_token_type('pg_catalog.default') t JOIN pg_ts_config_map m ON m.maptokentype=t.tokid JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE m.mapcfg=$1::regconfig", []string{"alias", "dictname"}, []string{"TEXT", "NAME"}, aliases, name)
	s.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceFTSSnowballConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		snowballConfigurationMaps(t, p, b, c, "pg_catalog.english")
		for _, tc := range []struct{ config, input, want string }{
			{"english", "in the list of stop words", "'list':3 'stop':5 'word':6"},
			{"english", "The Fat Rats", "'fat':2 'rat':3"},
			{"simple", "The Fat Rats", "'fat':2 'rats':3 'the':1"},
			{"english", "", ""},
			{"english", "Rats rats", "'rat':1,2"},
		} {
			for _, prepared := range []bool{false, true} {
				s := ftsConfigurationVector("SELECT to_tsvector($1::regconfig,$2::text) AS normalized", tc.want, tc.config, tc.input)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
				run(s)
			}
		}
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION snowball_copy (COPY=pg_catalog.english)", 0))
		snowballConfigurationMaps(t, p, b, c, "snowball_copy")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION snowball_copy ALTER MAPPING FOR asciiword WITH pg_catalog.simple", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('snowball_copy','The Fat Rats') AS normalized", "'fat':2 'rats':3 'the':1"))
		run(ftsConfigurationVector("SELECT to_tsvector('english','The Fat Rats') AS normalized", "'fat':2 'rat':3"))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY snowball_current (TEMPLATE=snowball,Language=english,StopWords=english)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION snowball_copy ALTER MAPPING FOR asciiword WITH snowball_current", 0))
		q := "SELECT to_tsvector('snowball_copy'::regconfig,$1::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'fat':2 'rat':3", "The Fat Rats"))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY snowball_current (stopwords)", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'fat':2 'rat':3 'the':1", "The Fat Rats"))
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION snowball_copy ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'fat':2 'rat':3 'the':1", "The Fat Rats"))
		run(fixture.Select("SELECT mapdict='snowball_current'::regdictionary::oid AS current,mapseqno FROM pg_ts_config_map WHERE mapcfg='snowball_copy'::regconfig AND maptokentype=1", []string{"current", "mapseqno"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		defer run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION snowball_copy ALTER MAPPING FOR word WITH public.unaccent,pg_catalog.english_stem", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'hotel':1", "hôtels"))
	})
}
