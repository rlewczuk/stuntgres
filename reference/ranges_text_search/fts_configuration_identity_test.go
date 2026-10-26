package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSConfigurationPreparedRenameMove(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "config_identity_destination")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA config_identity_destination", 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_identity_original (COPY=pg_catalog.simple)", 0))
		var original int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.config_identity_original'::regconfig::oid::bigint").Scan(&original); err != nil || original <= 0 {
			t.Fatal("original configuration identity", original, err)
		}
		query := "SELECT 'public.config_identity_original'::regconfig::oid::bigint AS id,to_tsvector('public.config_identity_original'::regconfig,$1::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		checkHeld := func(vector string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"id", "normalized"}, []string{"INT8", "TSVECTOR"}, [][]any{{original, vector}}, "The Fat Rats"))
		}
		checkHeld("'fat':2 'rats':3 'the':1")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_identity_original RENAME TO config_identity_renamed", 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_identity_original (PARSER=pg_catalog.default)", 0))
		checkHeld("'fat':2 'rats':3 'the':1")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_identity_renamed SET SCHEMA config_identity_destination", 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_identity_renamed (PARSER=pg_catalog.default)", 0))
		checkHeld("'fat':2 'rats':3 'the':1")
		var replacement, renamedReplacement int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.config_identity_original'::regconfig::oid::bigint,'public.config_identity_renamed'::regconfig::oid::bigint").Scan(&replacement, &renamedReplacement); err != nil || replacement <= 0 || renamedReplacement <= 0 || replacement == original || renamedReplacement == original || replacement == renamedReplacement {
			t.Fatal("reused spellings must have distinct identities", original, replacement, renamedReplacement, err)
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				name   string
				oid    int64
				vector string
			}{{"public.config_identity_original", replacement, ""}, {"public.config_identity_renamed", renamedReplacement, ""}, {"config_identity_destination.config_identity_renamed", original, "'fat':2 'rats':3 'the':1"}} {
				s := fixture.Select("SELECT $1::regconfig::oid::bigint AS id,to_tsvector($1::regconfig,$2::text) AS normalized", []string{"id", "normalized"}, []string{"INT8", "TSVECTOR"}, [][]any{{tc.oid, tc.vector}}, tc.name, "The Fat Rats")
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_identity_destination.config_identity_renamed DROP MAPPING FOR asciiword", 0))
		checkHeld("")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_identity_destination.config_identity_renamed ADD MAPPING FOR asciiword WITH pg_catalog.simple", 0))
		checkHeld("'fat':2 'rats':3 'the':1")
		// A fresh literal at the original spelling captures the replacement, not
		// the held handle's original object, even though its SQL text is identical.
		fresh := fixture.Select(query, []string{"id", "normalized"}, []string{"INT8", "TSVECTOR"}, [][]any{{replacement, ""}}, "The Fat Rats")
		run(fresh)
		run(fixture.Prepared(fresh))
	})
}

func TestReferenceFTSConfigurationPreparedSearchPath(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "config_identity_left")
	fixture.SchemaReferenceCleanup(t, "config_identity_right")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, query := range []string{
			"CREATE SCHEMA config_identity_left",
			"CREATE SCHEMA config_identity_right",
			"CREATE TEXT SEARCH CONFIGURATION config_identity_left.config_path (COPY=pg_catalog.simple)",
			"CREATE TEXT SEARCH CONFIGURATION config_identity_right.config_path (PARSER=pg_catalog.default)",
			"SET search_path TO config_identity_left,config_identity_right,public",
		} {
			run(fixture.Exec(query, 0))
		}
		var left, right int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'config_identity_left.config_path'::regconfig::oid::bigint,'config_identity_right.config_path'::regconfig::oid::bigint").Scan(&left, &right); err != nil || left <= 0 || right <= 0 || left == right {
			t.Fatal("competing path identities", left, right, err)
		}
		query := "SELECT 'config_path'::regconfig::oid::bigint AS id,to_tsvector('config_path'::regconfig,$1::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		check := func(oid int64, vector string) {
			s := fixture.Select(query, []string{"id", "normalized"}, []string{"INT8", "TSVECTOR"}, [][]any{{oid, vector}}, "The Fat Rats")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(s)
			run(fixture.Prepared(s))
		}
		check(left, "'fat':2 'rats':3 'the':1")
		run(fixture.Exec("SET search_path TO config_identity_right,config_identity_left,public", 0))
		check(right, "")
		run(fixture.Exec("SET search_path TO config_identity_left,config_identity_right,public", 0))
		check(left, "'fat':2 'rats':3 'the':1")
	})
}

func TestReferenceFTSConfigurationPreparedNamespaceRevoke(t *testing.T) {
	ftsConfigurationPreparedNamespace(t, false)
}

func TestReferenceFTSConfigurationPreparedNamespaceMove(t *testing.T) {
	ftsConfigurationPreparedNamespace(t, true)
}

func ftsConfigurationPreparedNamespace(t *testing.T, move bool) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"config_identity_reader"})
	fixture.SchemaReferenceCleanup(t, "config_identity_allowed")
	fixture.SchemaReferenceCleanup(t, "config_identity_denied")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		for _, query := range []string{
			"CREATE ROLE config_identity_reader",
			"CREATE SCHEMA config_identity_allowed",
			"CREATE SCHEMA config_identity_denied",
			"GRANT USAGE ON SCHEMA config_identity_allowed TO config_identity_reader",
			"CREATE TEXT SEARCH CONFIGURATION config_identity_allowed.config_authority (COPY=pg_catalog.simple)",
		} {
			run(fixture.Exec(query, 0))
		}
		var original int64
		if err := admin.QueryRowContext(p.Ctx, "SELECT 'config_identity_allowed.config_authority'::regconfig::oid::bigint").Scan(&original); err != nil || original <= 0 {
			t.Fatal("authorized original identity", original, err)
		}
		reader, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := reader.ExecContext(cleanup, "RESET ROLE"); err != nil {
				t.Errorf("reader role cleanup: %v", err)
			}
		}()
		readerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
		readerRun(fixture.Exec("SET ROLE config_identity_reader", 0))
		query := "SELECT to_tsvector('config_identity_allowed.config_authority'::regconfig,$1::text) AS normalized"
		stmt, err := reader.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: query}
		checkHeld := func(stage, vector string) {
			t.Helper()
			if !t.Run(stage, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConfigurationVector(query, vector, "The Fat Rats"))
			}) {
				t.FailNow()
			}
		}
		// Cross the normal custom-to-generic planning threshold before mutation,
		// without making the text argument or its conversion a constant.
		for _, stage := range []string{"warm_1", "warm_2", "warm_3", "warm_4", "warm_5", "warm_6"} {
			checkHeld(stage, "'fat':2 'rats':3 'the':1")
		}
		name, deniedSchema := "config_identity_allowed.config_authority", "config_identity_allowed"
		if move {
			run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_identity_allowed.config_authority SET SCHEMA config_identity_denied", 0))
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION config_identity_allowed.config_authority (PARSER=pg_catalog.default)", 0))
			name, deniedSchema = "config_identity_denied.config_authority", "config_identity_denied"
		} else {
			run(fixture.Exec("REVOKE USAGE ON SCHEMA config_identity_allowed FROM config_identity_reader", 0))
		}
		// Keep the post-mutation and post-grant observations separate: granting
		// schema USAGE can invalidate analysis independently of the object move.
		if move {
			checkHeld("held_after_move_and_spelling_reuse", "'fat':2 'rats':3 'the':1")
		} else {
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema "+deniedSchema)
			d.Position = "20"
			s := fixture.ErrorDiagnostic(query, d, "The Fat Rats")
			s.SelectRows = true
			s.ErrorStage = "execute"
			if !t.Run("held_after_usage_revoke", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}) {
				t.FailNow()
			}
		}
		literal := "SELECT to_tsvector('" + name + "'::regconfig,$1::text) AS normalized"
		for _, prepared := range []bool{false, true} {
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema "+deniedSchema)
			d.Position = "20"
			s := fixture.ErrorDiagnostic(literal, d, "The Fat Rats")
			s.SelectRows = true
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			} else {
				s.ErrorStage = "execute"
			}
			readerRun(s)
			runtime := ftsConfigurationError("SELECT to_tsvector($1::text::regconfig,$2::text) AS normalized", "42501", "permission denied for schema "+deniedSchema)
			runtime.Args = []any{name, "The Fat Rats"}
			runtime.SelectRows = true
			runtime.Prepared = prepared
			runtime.ErrorStage = "execute"
			readerRun(runtime)
		}
		if move {
			readerRun(ftsConfigurationVector(query, "", "The Fat Rats"))
			readerRun(fixture.Prepared(ftsConfigurationVector(query, "", "The Fat Rats")))
		}
		run(fixture.Exec("GRANT USAGE ON SCHEMA "+deniedSchema+" TO config_identity_reader", 0))
		if move {
			checkHeld("held_after_destination_usage_grant", "")
		} else {
			checkHeld("held_after_usage_restored", "'fat':2 'rats':3 'the':1")
		}
		readerRun(fixture.Select("SELECT $1::text::regconfig::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{original}}, name))
		readerRun(ftsConfigurationVector(literal, "'fat':2 'rats':3 'the':1", "The Fat Rats"))
		readerRun(fixture.Prepared(ftsConfigurationVector(literal, "'fat':2 'rats':3 'the':1", "The Fat Rats")))
	})
}
