package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTransformError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, code, message string, prepared bool) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.Prepared, s.ErrorStage = prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferencePGTransformDiagnostics(t *testing.T) {
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, position string }{
			{"SELECT missing FROM pg_catalog.pg_transform", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_transform", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: tc.position})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_transform WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}

var pgTransformMutations = []string{
	"INSERT INTO pg_catalog.pg_transform VALUES(0,23,12,0,0)",
	"UPDATE pg_catalog.pg_transform SET trftype=23 WHERE FALSE",
	"DELETE FROM pg_catalog.pg_transform WHERE FALSE",
	"TRUNCATE pg_catalog.pg_transform",
}

func TestReferencePGTransformPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ptr_reader"})
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		for _, q := range []string{
			"CREATE ROLE ptr_reader",
			"CREATE TABLE public.ptr_sentinel(v INT)",
			"GRANT USAGE ON SCHEMA public TO ptr_reader",
			"GRANT SELECT ON public.ptr_sentinel TO ptr_reader",
		} {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO public.ptr_sentinel VALUES(7)", 1))
		// The physically held DELETE is prepared under owner authority, then denied
		// under current reader authority even though no row can match.
		const mutation = "DELETE FROM pg_catalog.pg_transform WHERE FALSE"
		stmt, err := reader.PrepareContext(p.Ctx, mutation)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: mutation}
		readStmt, err := reader.PrepareContext(p.Ctx, pgTransformPairProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer readStmt.Close()
		readHeld := fixture.HeldStatement{Executor: reader, Statement: readStmt, SQL: pgTransformPairProbe}
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE ptr_reader", 0))
		defer reader.ExecContext(context.Background(), "RESET ROLE")
		pgTransformBoth(t, p, b, reader, pgTransformStep(b, "SELECT * FROM pg_catalog.pg_transform"))
		fixture.PgDescriptionExecutorRows(t, p, b, readHeld, pgTransformStep(b, pgTransformPairProbe, int64(23), int64(12)))
		for _, q := range pgTransformMutations {
			message := "permission denied for table pg_transform"
			if q == "TRUNCATE pg_catalog.pg_transform" {
				message = "permission denied: \"pg_transform\" is a system catalog"
			}
			for _, prepared := range []bool{false, true} {
				pgTransformError(t, p, b, reader, q, "42501", message, prepared)
			}
		}
		pgTransformError(t, p, b, held, mutation, "42501", "permission denied for table pg_transform", false)
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT v FROM public.ptr_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		pgTransformBoth(t, p, b, reader, pgTransformStep(b, "SELECT * FROM pg_catalog.pg_transform"))
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			for _, q := range pgTransformMutations {
				for _, prepared := range []bool{false, true} {
					pgTransformError(t, p, b, reader, q, "0A000", "catalog source mutation is unsupported", prepared)
				}
			}
		}
	})
}
