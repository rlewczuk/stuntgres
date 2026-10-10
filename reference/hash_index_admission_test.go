package reference_test

import "testing"

func TestReferenceHashIndexBuildAdmission(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "writer-rollback", true: "writer-commit"}[commit], func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(n INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(2),(NULL)", 2))
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2)", 1)
				for _, timer := range []struct{ setting, code, message string }{
					{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
					{"statement_timeout", "57014", "canceling statement due to statement timeout"},
				} {
					run(referenceExec("SET "+timer.setting+"='50ms'", 0))
					s := referencePrepared(referenceImplicitError("CREATE INDEX retry ON t USING hash(n)", referenceDiagnostic{Code: timer.code, Message: timer.message}))
					s.errorStage = "execute"
					run(s)
					run(referenceExec("SET "+timer.setting+"=0", 0))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				}
				var holder, waiter int
				if b.pg {
					holder = referenceLockPID(t, p.ctx, a)
					waiter = referenceLockPID(t, p.ctx, c)
				}
				done := referenceRowAsync(t, p.ctx, c, "CREATE INDEX retry ON t USING hash(n)", true, false)
				if b.pg {
					referenceLockWaitBehind(t, p.ctx, waiter, holder, referenceLockOID(t, p.ctx, "t"), "ShareLock")
				}
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				referenceLockExec(t, p.ctx, a, end, 0)
				r := referenceLockResult(t, done)
				referenceState(t, r.err, "")
				referenceLockAffected(t, r.result, 0)
				rows := [][]any{{int64(2)}}
				if commit {
					rows = append(rows, []any{int64(2)})
				}
				run(referenceSelect("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, rows))
				run(referenceSelect("SELECT n FROM t WHERE n IS NULL", []string{"n"}, []string{"INT4"}, [][]any{{nil}}))
				run(referenceExec("INSERT INTO t VALUES(2)", 1))
				rows = append(rows, []any{int64(2)})
				run(referencePrepared(referenceSelect("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, rows)))
			})
		})
	}
	t.Run("read-only-and-explicit-precedence", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
			a, c := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT)", 0))
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			referenceLockExec(t, p.ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			run(referenceExec("SET lock_timeout='50ms'", 0))
			for _, q := range []string{"CREATE INDEX retry ON t USING hash(n)", "CREATE INDEX retry ON absent_hash_table USING missing(n)"} {
				run(referenceExec("BEGIN READ ONLY", 0))
				s := referencePrepared(referenceImplicitError(q, referenceDiagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}))
				s.errorStage = "execute"
				run(s)
				run(referenceExec("ROLLBACK", 0))
			}
			run(referenceExec("SET lock_timeout=0", 0))
			referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
			run(referenceExec("BEGIN", 0))
			q := "CREATE INDEX retry ON t USING hash(n)"
			if b.pg {
				run(referencePrepared(referenceExec(q, 0)))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, [][]any{{"retry"}}))
			} else {
				s := referencePrepared(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceExec(q, 0))
		})
	})
	t.Run("cancellation", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
			a, c := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT)", 0))
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2)", 1)
			q := "CREATE INDEX retry ON t USING hash(n)"
			if b.pg {
				pid, holder := referenceLockPID(t, p.ctx, c), referenceLockPID(t, p.ctx, a)
				done := referenceRowAsync(t, p.ctx, c, q, true, false)
				referenceLockWaitBehind(t, p.ctx, pid, holder, referenceLockOID(t, p.ctx, "t"), "ShareLock")
				var canceled bool
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
					t.Fatal("cancel admitted build", canceled, err)
				}
				r := referenceLockResult(t, done)
				want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
				referenceAssertDiagnostic(t, r.err, &want)
				if r.result != nil {
					t.Fatal("canceled build published result")
				}
			} else {
				// This public witness covers timeout while waiting for admission.
				// Package-function build, lookup/residual, merge and final-veto
				// units cover the corresponding native execution boundaries.
				run(referenceExec("SET statement_timeout='50ms'", 0))
				run(referenceImplicitError(q, referenceDiagnostic{Code: "57014", Message: "canceling statement due to statement timeout"}))
				run(referenceExec("SET statement_timeout=0", 0))
			}
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			referenceLockExec(t, p.ctx, a, "COMMIT", 0)
			run(referenceExec(q, 0))
			run(referenceSelect("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	})
	t.Run("fresh-owner-and-schema", func(t *testing.T) {
		roleReferenceCleanup(t, []string{"hash_build_owner", "hash_build_actor"})
		schemaReferenceCleanup(t, "hash_build_acl")
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
			a, writer := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
			for _, q := range []string{"CREATE USER hash_build_owner", "CREATE USER hash_build_actor", "CREATE SCHEMA hash_build_acl AUTHORIZATION hash_build_owner"} {
				run(referenceExec(q, 0))
			}
			if b.pg {
				run(referenceExec("GRANT USAGE ON SCHEMA hash_build_acl TO hash_build_actor", 0))
				run(referenceExec("SET ROLE hash_build_owner", 0))
			}
			run(referenceExec("CREATE TABLE hash_build_acl.t(n INT)", 0))
			run(referenceExec("INSERT INTO hash_build_acl.t VALUES(2),(2)", 2))
			if b.pg {
				run(referenceExec("RESET ROLE", 0))
			}
			q := "CREATE INDEX retry ON hash_build_acl.t USING hash(n)"
			stmt, err := a.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: q}
			referenceLockExec(t, p.ctx, writer, "BEGIN", 0)
			referenceLockExec(t, p.ctx, writer, "INSERT INTO hash_build_acl.t VALUES(3)", 1)
			run(referenceExec("SET ROLE hash_build_actor", 0))
			run(referenceExec("SET lock_timeout='50ms'", 0))
			message := "must be owner of table t"
			if !b.pg {
				message = "permission denied for schema hash_build_acl"
			}
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: message})
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("SET lock_timeout=0", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			referenceLockExec(t, p.ctx, writer, "ROLLBACK", 0)
			if b.pg {
				run(referenceExec("REVOKE CREATE ON SCHEMA hash_build_acl FROM hash_build_owner,PUBLIC", 0))
				run(referenceExec("SET ROLE hash_build_owner", 0))
				referenceRunStep(t, p.ctx, held, b.recorder, referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for schema hash_build_acl"}))
				run(referenceExec("RESET ROLE", 0))
				run(referenceExec("GRANT CREATE ON SCHEMA hash_build_acl TO hash_build_owner", 0))
				run(referenceExec("SET ROLE hash_build_owner", 0))
			}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0))
			if b.pg {
				run(referenceExec("RESET ROLE", 0))
			}
			run(referenceSelect("SELECT n FROM hash_build_acl.t WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			stmt.Close()
			run(referenceExec("DROP SCHEMA hash_build_acl CASCADE", 0))
			run(referenceExec("DROP ROLE hash_build_actor", 0))
			run(referenceExec("DROP ROLE hash_build_owner", 0))
		})
	})
}
