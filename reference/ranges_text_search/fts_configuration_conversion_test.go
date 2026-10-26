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
