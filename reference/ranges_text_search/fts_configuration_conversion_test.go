package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSConfigurationConversion(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_conversion (COPY=pg_catalog.simple)", 0))
		query := "SELECT to_tsvector('public.config_conversion'::regconfig,$1::text) AS normalized"
		for _, tc := range []struct{ input, want string }{
			{"The Fat Rats", "'fat':2 'rats':3 'the':1"},
			{"Rats rats", "'rats':1,2"},
			{"", ""},
			{"   ", ""},
			{"Foo-Bar", "'bar':3 'foo':2 'foo-bar':1"},
			{"42 -7 3.14", "'-7':2 '3.14':3 '42':1"},
		} {
			run(ftsConfigurationVector(query, tc.want, tc.input))
			run(fixture.Prepared(ftsConfigurationVector(query, tc.want, []byte(tc.input))))
		}
		run(fixture.Select(query, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, nil))
		run(fixture.Select("SELECT to_tsvector(NULL::regconfig,'Words') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}))
		run(fixture.Select("SELECT to_tsvector('public.config_conversion'::regconfig,'Words') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.config_stop (TEMPLATE=pg_catalog.simple, Accept=false, StopWords=english)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_conversion ALTER MAPPING FOR asciiword WITH public.config_stop,pg_catalog.simple", 0))
		run(ftsConfigurationVector(query, "'yes':2", "The YeS"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_conversion ALTER MAPPING FOR asciiword WITH pg_catalog.simple,public.config_stop", 0))
		run(ftsConfigurationVector(query, "'the':1 'yes':2", "The YeS"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_conversion DROP MAPPING FOR asciiword", 0))
		// An unmapped word does not consume the position of the mapped integer.
		run(ftsConfigurationVector(query, "'42':1", "The 42"))
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_conversion ALTER MAPPING FOR word WITH public.unaccent,pg_catalog.simple", 0))
		run(ftsConfigurationVector(query, "'hotel':1", "Hôtel"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_conversion ALTER MAPPING FOR word WITH public.unaccent", 0))
		run(ftsConfigurationVector(query, "", "Hôtel"))
	})
}

func TestReferenceFTSConfigurationPrepared(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_prepared (COPY=pg_catalog.simple)", 0))
		runtimeSQL := "SELECT to_tsvector('public.config_prepared'::regconfig,$1::text) AS normalized"
		constantSQL := "SELECT to_tsvector('public.config_prepared'::regconfig,'The Fat Rats') AS normalized"
		runtime, err := conn.PrepareContext(p.Ctx, runtimeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		constant, err := conn.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constant.Close()
		runtimeHeld := fixture.HeldStatement{Executor: conn, Statement: runtime, SQL: runtimeSQL}
		constantHeld := fixture.HeldStatement{Executor: conn, Statement: constant, SQL: constantSQL}
		known := "'fat':2 'rats':3 'the':1"
		for range 6 {
			fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, ftsConfigurationVector(runtimeSQL, known, "The Fat Rats"))
			fixture.RunStep(t, p.Ctx, constantHeld, b.Recorder, ftsConfigurationVector(constantSQL, known))
		}
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_prepared DROP MAPPING FOR asciiword", 0))
		fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, ftsConfigurationVector(runtimeSQL, "", "The Fat Rats"))
		// The warmed immutable all-constant plan retains its prior folded vector;
		// the runtime text parameter above reads the changed mapping instead.
		fixture.RunStep(t, p.Ctx, constantHeld, b.Recorder, ftsConfigurationVector(constantSQL, known))
		run(ftsConfigurationVector(constantSQL, ""))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_prepared ADD MAPPING FOR asciiword WITH pg_catalog.simple", 0))
		fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, ftsConfigurationVector(runtimeSQL, known, "The Fat Rats"))
		run(ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION public.config_prepared ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`))
		fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, ftsConfigurationVector(runtimeSQL, known, "The Fat Rats"))
		var capturedOID int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'public.config_prepared'::regconfig::oid::bigint").Scan(&capturedOID); err != nil || capturedOID <= 0 {
			t.Fatal("captured configuration identity", capturedOID, err)
		}
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_prepared", 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_prepared (PARSER=pg_catalog.default)", 0))
		stale := ftsConfigurationError(runtimeSQL, "XX000", fmt.Sprintf("cache lookup failed for text search configuration %d", capturedOID))
		stale.Args = []any{"The Fat Rats"}
		stale.SelectRows = true
		stale.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, stale)
		run(ftsConfigurationVector(runtimeSQL, "", "The Fat Rats"))
	})
}

func TestReferenceUnaccentDictionaryFiltering(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE EXTENSION unaccent",
			"CREATE TEXT SEARCH CONFIGURATION unaccent_filter_config (COPY=pg_catalog.simple)",
			"CREATE TEXT SEARCH DICTIONARY unaccent_filter_stop (TEMPLATE=pg_catalog.simple,stopwords=english,accept=false)",
			"ALTER TEXT SEARCH CONFIGURATION unaccent_filter_config ALTER MAPPING FOR word,asciiword,hword,asciihword,hword_part,hword_asciipart WITH public.unaccent,pg_catalog.simple",
		} {
			run(fixture.Exec(q, 0))
		}
		q := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ input, want string }{
			{"Hôtel", "'hotel':1"},
			{"plain", "'plain':1"},
			{"Hôtel Hôtel plain", "'hotel':1,2 'plain':3"},
			{"Hôtel-plain", "'hotel':2 'hotel-plain':1 'plain':3"},
			{"", ""},
		} {
			s := ftsConfigurationVector(q, tc.want, "unaccent_filter_config", tc.input)
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, "unaccent_filter_config", nil))
		run(fixture.Select(q+" WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil, "unaccent_filter_config", "Hôtel"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION unaccent_filter_config ALTER MAPPING FOR word,asciiword WITH public.unaccent", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "", "unaccent_filter_config", "Hôtel plain"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION unaccent_filter_config ALTER MAPPING FOR word,asciiword WITH pg_catalog.simple,public.unaccent", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'hôtel':1 'plain':2", "unaccent_filter_config", "Hôtel plain"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION unaccent_filter_config ALTER MAPPING FOR word,asciiword WITH public.unaccent,public.unaccent_filter_stop,pg_catalog.simple", 0))
		// The transformed Thé is a stopword, so the final simple dictionary
		// must not see it; unrecognized YeS continues to the final dictionary.
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'hotel':3 'yes':2", "unaccent_filter_config", "Thé YeS Hôtel"))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.unaccent RENAME TO unaccent_filter_moved", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(q, "'hotel':1", "unaccent_filter_config", "Hôtel"))
	})
}
