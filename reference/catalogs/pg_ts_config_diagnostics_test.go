package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTSConfigError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, code, message string, prepared bool) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.Prepared, s.ErrorStage = prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferencePGTSConfigDiagnostics(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_ts_config", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_ts_config", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_ts_config WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_catalog.pg_ts_config WHERE FALSE", "22003", "value \"4294967296\" is out of range for type oid", "8"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_ts_config FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}

var pgTSConfigMutations = []string{
	"INSERT INTO pg_catalog.pg_ts_config SELECT * FROM pg_catalog.pg_ts_config WHERE FALSE",
	"UPDATE pg_catalog.pg_ts_config SET cfgowner=10 WHERE FALSE",
	"UPDATE pg_catalog.pg_ts_config SET cfgowner=nextval('public.ptsc_effect')::oid WHERE oid=3748",
	"DELETE FROM pg_catalog.pg_ts_config WHERE FALSE",
	"WITH denied AS (DELETE FROM pg_catalog.pg_ts_config WHERE FALSE RETURNING oid) SELECT * FROM denied",
	"MERGE INTO pg_catalog.pg_ts_config c USING (SELECT 3748::oid AS id) s ON c.oid=s.id WHEN MATCHED THEN UPDATE SET cfgowner=10",
	"TRUNCATE pg_catalog.pg_ts_config",
}

func pgTSConfigSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM public.ptsc_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT last_value,is_called FROM public.ptsc_effect", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
}

func TestReferencePGTSConfigPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ptsc_reader"})
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		for _, q := range []string{
			"CREATE ROLE ptsc_reader",
			"CREATE TABLE public.ptsc_sentinel(v INT)",
			"GRANT SELECT ON public.ptsc_sentinel TO ptsc_reader",
			"GRANT USAGE ON SCHEMA public TO ptsc_reader",
			// Supported default privileges establish genuine sequence authority.
			"ALTER DEFAULT PRIVILEGES GRANT USAGE,SELECT ON SEQUENCES TO ptsc_reader",
			"CREATE SEQUENCE public.ptsc_effect",
			"ALTER DEFAULT PRIVILEGES REVOKE USAGE,SELECT ON SEQUENCES FROM ptsc_reader",
		} {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO public.ptsc_sentinel VALUES(7)", 1))
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE ptsc_reader", 0))
		defer reader.ExecContext(context.Background(), "RESET ROLE")
		pgTSConfigInventory(t, p, b, reader)
		for _, q := range pgTSConfigMutations {
			message := "permission denied for table pg_ts_config"
			if q == "TRUNCATE pg_catalog.pg_ts_config" {
				message = "permission denied: \"pg_ts_config\" is a system catalog"
			}
			for _, prepared := range []bool{false, true} {
				pgTSConfigError(t, p, b, reader, q, "42501", message, prepared)
				pgTSConfigSentinel(t, p, b, reader)
				pgTSConfigInventory(t, p, b, reader)
			}
		}
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("RESET ROLE", 0))
		for _, q := range []string{"UPDATE pg_catalog.pg_ts_config SET cfgowner=10 WHERE FALSE", "DELETE FROM pg_catalog.pg_ts_config WHERE FALSE"} {
			if b.PG {
				tx, err := reader.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q, 0))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, prepared := range []bool{false, true} {
					pgTSConfigError(t, p, b, reader, q, "0A000", "catalog source mutation is unsupported", prepared)
				}
			}
		}
		if !b.PG {
			for _, q := range []string{pgTSConfigMutations[0], pgTSConfigMutations[4], pgTSConfigMutations[5], pgTSConfigMutations[6]} {
				for _, prepared := range []bool{false, true} {
					pgTSConfigError(t, p, b, reader, q, "0A000", "catalog source mutation is unsupported", prepared)
				}
			}
		}
		pgTSConfigSentinel(t, p, b, reader)
		pgTSConfigInventory(t, p, b, reader)
	})
}

func pgTSConfigCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement) {
	t.Helper()
	if b.PG {
		return // pq transport precancellation does not promise this native API contract.
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(ctx, pgTSConfigNameProbe, "simple")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled query returned Rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("query cancellation", err)
	}
	stmt, err := b.DB.PrepareContext(ctx, pgTSConfigNameProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled prepare returned Stmt")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("prepare cancellation", err)
	}
	rows, err = held.QueryContext(ctx, pgTSConfigNameProbe, "simple")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled held query returned Rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("held cancellation", err)
	}
}

func TestReferencePGTSConfigPrepared(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ptsc_held_reader"})
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, conn := fixture.LockConnections(t, p.Ctx, b.DB)
		for _, tc := range []struct {
			q      string
			values []any
		}{
			{pgTSConfigNameProbe, []any{"simple", pgTSConfigAbsent, nil, "simple"}},
			{pgTSConfigOIDProbe, []any{int64(3748), int64(0), nil, int64(3748)}},
		} {
			stmt, err := conn.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}
			if tc.q == pgTSConfigNameProbe {
				pgTSConfigCanceled(t, p, b, held)
			}
			for _, value := range tc.values {
				want := [][]any{}
				if value == "simple" || value == int64(3748) {
					want = pgTSConfigSimple(b)
				}
				s := pgTSConfigStep(b, tc.q, want, value)
				pgTSConfigBoth(t, p, b, conn, s)
				fixture.PgDescriptionExecutorRows(t, p, b, held, s)
			}
		}
		const mutation = "UPDATE pg_catalog.pg_ts_config SET cfgowner=10 WHERE FALSE"
		stmt, err := conn.PrepareContext(p.Ctx, mutation)
		if err != nil {
			t.Fatal("superuser prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: mutation}
		for _, q := range []string{"CREATE ROLE ptsc_held_reader", "SET ROLE ptsc_held_reader"} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
		}
		defer conn.ExecContext(context.Background(), "RESET ROLE")
		pgTSConfigError(t, p, b, held, mutation, "42501", "permission denied for table pg_ts_config", false)
		pgTSConfigInventory(t, p, b, conn)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		if b.PG {
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(mutation, 0))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		} else {
			pgTSConfigError(t, p, b, held, mutation, "0A000", "catalog source mutation is unsupported", false)
		}
	})
}
