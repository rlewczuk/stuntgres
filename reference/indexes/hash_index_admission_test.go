package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashIndexBuildAdmission(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "writer-rollback", true: "writer-commit"}[commit], func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2),(NULL)", 2))
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2)", 1)
				for _, timer := range []struct{ setting, code, message string }{
					{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
					{"statement_timeout", "57014", "canceling statement due to statement timeout"},
				} {
					run(fixture.Exec("SET "+timer.setting+"='50ms'", 0))
					s := fixture.Prepared(fixture.ImplicitError("CREATE INDEX retry ON t USING hash(n)", fixture.Diagnostic{Code: timer.code, Message: timer.message}))
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Exec("SET "+timer.setting+"=0", 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				}
				var holder, waiter int
				if b.PG {
					holder = fixture.LockPID(t, p.Ctx, a)
					waiter = fixture.LockPID(t, p.Ctx, c)
				}
				done := fixture.RowAsync(t, p.Ctx, c, "CREATE INDEX retry ON t USING hash(n)", true, false)
				if b.PG {
					fixture.LockWaitBehind(t, p.Ctx, waiter, holder, fixture.LockOID(t, p.Ctx, "t"), "ShareLock")
				}
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				fixture.LockExec(t, p.Ctx, a, end, 0)
				r := fixture.LockResult(t, done)
				fixture.State(t, r.Err, "")
				fixture.LockAffected(t, r.Result, 0)
				rows := [][]any{{int64(2)}}
				if commit {
					rows = append(rows, []any{int64(2)})
				}
				run(fixture.Select("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, rows))
				run(fixture.Select("SELECT n FROM t WHERE n IS NULL", []string{"n"}, []string{"INT4"}, [][]any{{nil}}))
				run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
				rows = append(rows, []any{int64(2)})
				run(fixture.Prepared(fixture.Select("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, rows)))
			})
		})
	}
	t.Run("read-only-and-explicit-precedence", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			run(fixture.Exec("SET lock_timeout='50ms'", 0))
			for _, q := range []string{"CREATE INDEX retry ON t USING hash(n)", "CREATE INDEX retry ON absent_hash_table USING missing(n)"} {
				run(fixture.Exec("BEGIN READ ONLY", 0))
				s := fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}))
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("ROLLBACK", 0))
			}
			run(fixture.Exec("SET lock_timeout=0", 0))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			run(fixture.Exec("BEGIN", 0))
			q := "CREATE INDEX retry ON t USING hash(n)"
			if b.PG {
				run(fixture.Prepared(fixture.Exec(q, 0)))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, [][]any{{"retry"}}))
			} else {
				s := fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec(q, 0))
		})
	})
	t.Run("cancellation", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2)", 1)
			q := "CREATE INDEX retry ON t USING hash(n)"
			if b.PG {
				pid, holder := fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, a)
				done := fixture.RowAsync(t, p.Ctx, c, q, true, false)
				fixture.LockWaitBehind(t, p.Ctx, pid, holder, fixture.LockOID(t, p.Ctx, "t"), "ShareLock")
				var canceled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
					t.Fatal("cancel admitted build", canceled, err)
				}
				r := fixture.LockResult(t, done)
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, r.Err, &want)
				if r.Result != nil {
					t.Fatal("canceled build published result")
				}
			} else {
				// This public witness covers timeout while waiting for admission.
				// Package-function build, lookup/residual, merge and final-veto
				// units cover the corresponding native execution boundaries.
				run(fixture.Exec("SET statement_timeout='50ms'", 0))
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout"}))
				run(fixture.Exec("SET statement_timeout=0", 0))
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			run(fixture.Exec(q, 0))
			run(fixture.Select("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	})
	t.Run("fresh-owner-and-schema", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"hash_build_owner", "hash_build_actor"})
		fixture.SchemaReferenceCleanup(t, "hash_build_acl")
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
			a, writer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			for _, q := range []string{"CREATE USER hash_build_owner", "CREATE USER hash_build_actor", "CREATE SCHEMA hash_build_acl AUTHORIZATION hash_build_owner"} {
				run(fixture.Exec(q, 0))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA hash_build_acl TO hash_build_actor", 0))
				run(fixture.Exec("SET ROLE hash_build_owner", 0))
			}
			run(fixture.Exec("CREATE TABLE hash_build_acl.t(n INT)", 0))
			run(fixture.Exec("INSERT INTO hash_build_acl.t VALUES(2),(2)", 2))
			if b.PG {
				run(fixture.Exec("RESET ROLE", 0))
			}
			q := "CREATE INDEX retry ON hash_build_acl.t USING hash(n)"
			stmt, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
			fixture.LockExec(t, p.Ctx, writer, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, writer, "INSERT INTO hash_build_acl.t VALUES(3)", 1)
			run(fixture.Exec("SET ROLE hash_build_actor", 0))
			run(fixture.Exec("SET lock_timeout='50ms'", 0))
			message := "must be owner of table t"
			if !b.PG {
				message = "permission denied for schema hash_build_acl"
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET lock_timeout=0", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			fixture.LockExec(t, p.Ctx, writer, "ROLLBACK", 0)
			if b.PG {
				run(fixture.Exec("REVOKE CREATE ON SCHEMA hash_build_acl FROM hash_build_owner,PUBLIC", 0))
				run(fixture.Exec("SET ROLE hash_build_owner", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema hash_build_acl"}))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT CREATE ON SCHEMA hash_build_acl TO hash_build_owner", 0))
				run(fixture.Exec("SET ROLE hash_build_owner", 0))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			if b.PG {
				run(fixture.Exec("RESET ROLE", 0))
			}
			run(fixture.Select("SELECT n FROM hash_build_acl.t WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			stmt.Close()
			run(fixture.Exec("DROP SCHEMA hash_build_acl CASCADE", 0))
			run(fixture.Exec("DROP ROLE hash_build_actor", 0))
			run(fixture.Exec("DROP ROLE hash_build_owner", 0))
		})
	})
}
