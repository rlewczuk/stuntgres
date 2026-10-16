package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func pgAggregateError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, code, message string, prepared bool) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.Prepared, s.ErrorStage = prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
func TestReferencePGAggregateDiagnostics(t *testing.T) {
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, pos string }{
			{"SELECT missing FROM pg_catalog.pg_aggregate", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_aggregate", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT oid FROM pg_catalog.pg_aggregate", "42703", "column \"oid\" does not exist", "8"},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_aggregate WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_catalog.pg_aggregate WHERE FALSE", "22003", "value \"4294967296\" is out of range for type oid", "8"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}

var pgAggregateMutations = []string{
	"INSERT INTO pg_catalog.pg_aggregate SELECT * FROM pg_catalog.pg_aggregate WHERE FALSE",
	"INSERT INTO pg_catalog.pg_aggregate SELECT * FROM pg_catalog.pg_aggregate WHERE aggfnoid::oid=2803",
	"UPDATE pg_catalog.pg_aggregate SET aggtransspace=nextval('public.pga_authority_effect')::int4 WHERE aggfnoid::oid=2803",
	"UPDATE pg_catalog.pg_aggregate SET aggtransspace=0 WHERE FALSE",
	"UPDATE pg_catalog.pg_aggregate SET aggtransspace=0 WHERE aggfnoid::oid=2803",
	"DELETE FROM pg_catalog.pg_aggregate WHERE FALSE",
	"DELETE FROM pg_catalog.pg_aggregate WHERE aggfnoid::oid=2803",
	"WITH denied AS (DELETE FROM pg_catalog.pg_aggregate WHERE FALSE RETURNING aggfnoid) SELECT * FROM denied",
	"TRUNCATE pg_catalog.pg_aggregate",
	"MERGE INTO pg_catalog.pg_aggregate a USING (SELECT 2803 AS id) s ON a.aggfnoid::oid=s.id WHEN MATCHED THEN UPDATE SET aggtransspace=0",
}

func pgAggregateSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM public.pga_authority_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT last_value,is_called FROM public.pga_authority_effect", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
}
func pgAggregatePermissions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
	for _, q := range []string{"CREATE ROLE pga_authority_reader", "CREATE TABLE public.pga_authority_sentinel(v INT)", "INSERT INTO public.pga_authority_sentinel VALUES(7)", "GRANT SELECT ON public.pga_authority_sentinel TO pga_authority_reader"} {
		n := int64(0)
		if q == "INSERT INTO public.pga_authority_sentinel VALUES(7)" {
			n = 1
		}
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(q, n))
	}
	// Explicit sequence GRANT remains outside native SQL. Populate the same
	// real sequence ACL through supported creation-time default privileges,
	// then retire the default grant without changing the created sequence.
	for _, q := range []string{
		"ALTER DEFAULT PRIVILEGES GRANT USAGE,SELECT ON SEQUENCES TO pga_authority_reader",
		"CREATE SEQUENCE public.pga_authority_effect",
		"ALTER DEFAULT PRIVILEGES REVOKE USAGE,SELECT ON SEQUENCES FROM pga_authority_reader",
	} {
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(q, 0))
	}
	if b.PG {
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO pga_authority_reader", 0))
	} else {
		// Schema SQL grants are not native syntax. Transfer only namespace
		// visibility; the sentinel's explicit table/sequence ACLs remain real.
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("ALTER SCHEMA public OWNER TO pga_authority_reader", 0))
	}
	fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE pga_authority_reader", 0))
	defer reader.ExecContext(context.Background(), "RESET ROLE")
	pgAggregateDefinitions(t, p, b, reader)
	for _, q := range []string{"UPDATE pg_catalog.pg_aggregate SET missing=0 WHERE FALSE", "INSERT INTO pg_catalog.pg_aggregate(missing) VALUES(0)"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: "column \"missing\" of relation \"pg_aggregate\" does not exist", Position: strconv.Itoa(strings.Index(q, "missing") + 1)})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
		}
	}
	for _, q := range pgAggregateMutations {
		message := "permission denied for table pg_aggregate"
		if q == "TRUNCATE pg_catalog.pg_aggregate" {
			message = "permission denied: \"pg_aggregate\" is a system catalog"
		}
		for _, prepared := range []bool{false, true} {
			pgAggregateError(t, p, b, reader, q, "42501", message, prepared)
			pgAggregateSentinel(t, p, b, reader)
		}
	}
	pgAggregateDefinitions(t, p, b, reader)
	fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("RESET ROLE", 0))
	// PostgreSQL superusers may write catalogs. Only zero-row operations are
	// exercised, inside rollback; native immutability is a distinct boundary.
	for _, q := range []string{"UPDATE pg_catalog.pg_aggregate SET aggtransspace=0 WHERE FALSE", "DELETE FROM pg_catalog.pg_aggregate WHERE FALSE"} {
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
				pgAggregateError(t, p, b, reader, q, "0A000", "catalog source mutation is unsupported", prepared)
			}
		}
	}
	pgAggregateSentinel(t, p, b, reader)
}
func TestReferencePGAggregatePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pga_authority_reader"})
	pgAggregateScenario(t, pgAggregatePermissions)
}
func pgAggregateCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement) {
	t.Helper()
	if b.PG {
		// Fast pq precancellation is transport-racy and does not promise nil
		// outputs; this ordinary context-error contract belongs to native.
		return
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(ctx, pgAggregateProbe, "2803")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled query returned Rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("query cancellation", err)
	}
	stmt, err := b.DB.PrepareContext(ctx, pgAggregateProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled prepare returned Stmt")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("prepare cancellation", err)
	}
	rows, err = held.QueryContext(ctx, pgAggregateProbe, "2803")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled held query returned Rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("held cancellation", err)
	}
}
func TestReferencePGAggregatePrepared(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pga_held_reader"})
	fixture.SchemaReferenceCleanup(t, "pga_held_addon")
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, c := fixture.LockConnections(t, p.Ctx, b.DB)
		stmt, err := c.PrepareContext(p.Ctx, pgAggregateProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: pgAggregateProbe}
		pgAggregateCanceled(t, p, b, held)
		for _, v := range []any{"2803", nil, "0", "2803"} {
			want := [][]any{}
			if v == "2803" {
				want = pgAggregateCountRaw()
			}
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, want, v))
		}
		// Parameter admission fails before an empty cursor can be published and the
		// same held statement remains usable with a fresh valid frame afterwards.
		rows, err := held.QueryContext(p.Ctx, pgAggregateProbe, "4294967296")
		if rows != nil {
			rows.Close()
			t.Fatal("invalid REGPROC frame returned Rows")
		}
		want := fixture.ControlsReferenceDiagnostic("22003", "value \"4294967296\" is out of range for type oid")
		want.Context = "unnamed portal parameter $1 = '...'"
		fixture.AssertDiagnostic(t, err, &want)
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, pgAggregateCountRaw(), "2803"))
		const mutation = "UPDATE pg_catalog.pg_aggregate SET aggtransspace=0 WHERE FALSE"
		command, err := c.PrepareContext(p.Ctx, mutation)
		if err != nil {
			t.Fatal("superuser prepare", err)
		}
		defer command.Close()
		cmd := fixture.HeldStatement{Executor: c, Statement: command, SQL: mutation}
		for _, q := range []string{"CREATE ROLE pga_held_reader", "CREATE SCHEMA pga_held_addon", "CREATE EXTENSION intagg SCHEMA pga_held_addon", "SET ROLE pga_held_reader", "SET search_path TO public"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		defer c.ExecContext(context.Background(), "RESET ROLE")
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, pgAggregateCountRaw(), "2803"))
		pgAggregateError(t, p, b, cmd, mutation, "42501", "permission denied for table pg_aggregate", false)
		readerCommand, err := c.PrepareContext(p.Ctx, mutation)
		if err != nil {
			t.Fatal("reader prepare", err)
		}
		defer readerCommand.Close()
		readerCmd := fixture.HeldStatement{Executor: c, Statement: readerCommand, SQL: mutation}
		pgAggregateError(t, p, b, readerCmd, mutation, "42501", "permission denied for table pg_aggregate", false)
		installed := pgAggregateIntaggCheck(t, p, b, c, "pga_held_addon")
		raw := [][]any{{"pga_held_addon.int_array_aggregate", "n", int64(0), "pga_held_addon.int_agg_state", "pga_held_addon.int_agg_final_array", "-", "-", "-", "-", "-", "-", false, false, "r", "r", int64(0), int64(2281), int64(0), int64(0), int64(0), nil, nil}}
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, raw, strconv.FormatInt(installed[0], 10)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		for _, e := range []fixture.Executor{cmd, readerCmd} {
			if b.PG {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(mutation, 0))
			} else {
				pgAggregateError(t, p, b, e, mutation, "0A000", "catalog source mutation is unsupported", false)
			}
		}
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, pgAggregateCountRaw(), "2803"))
	})
}
