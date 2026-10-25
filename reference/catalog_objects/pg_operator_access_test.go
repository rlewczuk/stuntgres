package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGOperatorDiagnostics(t *testing.T) {
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, position string }{
			{"SELECT missing FROM pg_catalog.pg_operator", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_operator", "14"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: tc.position})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s.Prepared, s.ErrorStage = true, "prepare"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_diag(n INT)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_diag VALUES(7)", 1))
		s := fixture.ImplicitError("SELECT 1 + TRUE FROM operator_diag", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		s.Prepared, s.ErrorStage = true, "prepare"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n FROM operator_diag", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferencePGOperatorPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"operator_catalog_reader"})
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE ROLE operator_catalog_reader", 0))
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE TABLE operator_permission_sentinel(n INT)", 0))
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO operator_permission_sentinel VALUES(7)", 1))
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE operator_catalog_reader", 0))
		defer reader.ExecContext(p.Ctx, "RESET ROLE")
		s := fixture.Select("SELECT oid,oprname,oprcode FROM pg_operator WHERE oid=551", []string{"oid", "oprname", "oprcode"}, []string{"OID", "NAME", "REGPROC"}, [][]any{{int64(551), "+", "int4pl"}})
		fixture.PgDescriptionExecutorRows(t, p, b, reader, s)
		fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Prepared(s))
		for _, tc := range []struct{ q, nativePrepareStage string }{
			{"INSERT INTO pg_operator(oid) VALUES(999999)", "execute"},
			{"UPDATE pg_operator SET oprcanmerge=false WHERE oid=551", "prepare"},
			{"DELETE FROM pg_operator WHERE oid=551", "execute"},
		} {
			for _, prepared := range []bool{false, true} {
				d := fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_operator"}
				stage := "execute"
				if !b.PG {
					d = fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"}
					if prepared {
						stage = tc.nativePrepareStage
					}
				}
				s := fixture.ImplicitError(tc.q, d)
				s.Prepared, s.ErrorStage = prepared, stage
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select("SELECT n FROM operator_permission_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
				pgOperatorInventory(t, p, b)
			}
		}
		pgOperatorInventory(t, p, b)
	})
}

func pgOperatorHeldExecution(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct {
		query     string
		hit, miss any
	}{
		{"SELECT oid,oprname,oprcode FROM pg_operator WHERE oid=$1::oid", int64(551), int64(0)},
		{"SELECT oid,oprname,oprcode FROM pg_operator WHERE oprname=$1::name AND oprleft=23 AND oprright=23", "+", "absent"},
		{"SELECT oid,oprname,oprcode FROM pg_operator WHERE oprname='+' AND oprleft=$1::regtype::oid AND oprright=23", "integer", "boolean"},
	} {
		stmt, err := b.DB.PrepareContext(p.Ctx, tc.query)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: tc.query}
		for _, arg := range []any{tc.hit, tc.miss, nil, tc.hit} {
			var want [][]any
			if arg == tc.hit {
				want = [][]any{{int64(551), "+", "int4pl"}}
			}
			s := fixture.Select(tc.query, []string{"oid", "oprname", "oprcode"}, []string{"OID", "NAME", "REGPROC"}, want, arg)
			fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, tc.hit)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query returned rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled query", err)
			}
			fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(tc.query, []string{"oid", "oprname", "oprcode"}, []string{"OID", "NAME", "REGPROC"}, [][]any{{int64(551), "+", "int4pl"}}, tc.hit))
		}
		failedRows, argumentError := stmt.QueryContext(p.Ctx, struct{}{})
		if failedRows != nil {
			failedRows.Close()
			t.Fatal("invalid Go argument returned rows")
		}
		var sqlError interface{ SQLState() string }
		if argumentError == nil || errors.As(argumentError, &sqlError) {
			t.Fatal("invalid Go argument must remain ordinary", argumentError)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(tc.query, []string{"oid", "oprname", "oprcode"}, []string{"OID", "NAME", "REGPROC"}, [][]any{{int64(551), "+", "int4pl"}}, tc.hit))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferencePGOperatorHeldExecution(t *testing.T) {
	pgOperatorScenario(t, pgOperatorHeldExecution)
}
