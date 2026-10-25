package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceMaintLifecycle(t *testing.T) {
	t.Run("HeldConsumers", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("CREATE TABLE g() INHERITS(c)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES(2)", 1)
			s.exec("INSERT INTO g VALUES(3)", 1)
			s.exec("CREATE VIEW topology AS SELECT a FROM p", 0)
			queries := []string{"SELECT a FROM p ORDER BY a", "SELECT a FROM ONLY p ORDER BY a", "SELECT a FROM topology ORDER BY a"}
			var checks []func([][]any)
			for _, q := range queries {
				stmt, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					s.t.Fatal(err)
				}
				defer stmt.Close()
				x := fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: q}
				checks = append(checks, func(want [][]any) {
					fixture.RunStep(s.t, s.p.Ctx, x, s.record, fixture.Select(q, []string{"a"}, []string{"INT4"}, want))
				})
			}
			for _, check := range checks {
				check([][]any{{int64(1)}})
			}
			s.exec("ALTER TABLE c INHERIT p", 0)
			checks[0]([][]any{{int64(1)}, {int64(2)}, {int64(3)}})
			checks[1]([][]any{{int64(1)}})
			checks[2]([][]any{{int64(1)}, {int64(2)}, {int64(3)}})
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			for _, check := range checks {
				check([][]any{{int64(1)}})
			}
			s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
		})
	})
	t.Run("TransactionSavepointAndCommit", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES(2)", 1)
			peer, err := s.b.DB.Conn(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer peer.Close()
			peerValues := func(want [][]any) {
				step := fixture.Select("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, want)
				step.Prepared = s.prepared
				fixture.RunStep(s.t, s.p.Ctx, peer, s.b.Recorder, step)
			}
			s.exec("BEGIN", 0)
			s.exec("SAVEPOINT before_attach", 0)
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			peerValues([][]any{{int64(1)}})
			s.exec("ROLLBACK TO SAVEPOINT before_attach", 0)
			s.edges("c")
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.exec("COMMIT", 0)
			peerValues([][]any{{int64(1)}, {int64(2)}})
			s.exec("BEGIN", 0)
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.values("SELECT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
			s.exec("ROLLBACK", 0)
			s.edges("c", "p")
			peerValues([][]any{{int64(1)}, {int64(2)}})
			s.exec("BEGIN", 0)
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.exec("COMMIT", 0)
			peerValues([][]any{{int64(1)}})
			inheritanceAlterAttributes(s, "c", [][]any{{"a", int64(1), true, int64(0)}})
		})
	})
	t.Run("CanceledExecution", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("INSERT INTO c VALUES(2)", 1)
			const consumer = "SELECT a FROM c"
			held, err := s.c.PrepareContext(s.p.Ctx, consumer)
			if err != nil {
				s.t.Fatal(err)
			}
			defer held.Close()
			canceled := func(q string) {
				if s.b.PG {
					// pq's fast pre-cancel outcome races transport execution.
					// Observe an actual parent-lock wait, then cancel on-server.
					holder, err := s.b.DB.Conn(s.p.Ctx)
					if err != nil {
						s.t.Fatal(err)
					}
					defer holder.Close()
					defer holder.ExecContext(s.p.Ctx, "ROLLBACK")
					fixture.LockExec(s.t, s.p.Ctx, holder, "BEGIN", 0)
					fixture.LockExec(s.t, s.p.Ctx, holder, "LOCK TABLE ONLY p IN ACCESS EXCLUSIVE MODE", 0)
					pid := fixture.LockPID(s.t, s.p.Ctx, s.c)
					blocker := fixture.LockPID(s.t, s.p.Ctx, holder)
					done := fixture.RowAsync(s.t, s.p.Ctx, s.c, q, s.prepared, false)
					fixture.RowWait(s.t, s.p.Ctx, pid, blocker)
					var acknowledged bool
					if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&acknowledged); err != nil || !acknowledged {
						s.t.Fatal("server cancellation acknowledgement", acknowledged, err)
					}
					outcome := fixture.LockResult(s.t, done)
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(s.t, outcome.Err, &want)
					if outcome.Result != nil {
						s.t.Fatal("canceled relationship returned result", outcome.Result)
					}
				} else {
					// The native public adapter promises an ordinary context error
					// before execution, rather than a fabricated PostgreSQL record.
					ctx, cancel := context.WithCancel(s.p.Ctx)
					cancel()
					if outcome := fixture.RowExecute(ctx, s.c, q, s.prepared); !errors.Is(outcome.Err, context.Canceled) || outcome.Result != nil {
						s.t.Fatal("native pre-canceled relationship boundary", outcome)
					}
				}
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: consumer}, s.record, fixture.Select(consumer, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
			}
			canceled("ALTER TABLE c INHERIT p")
			s.edges("c")
			s.exec("ALTER TABLE c INHERIT p", 0)
			canceled("ALTER TABLE c NO INHERIT p")
			s.edges("c", "p")
		})
	})
}
