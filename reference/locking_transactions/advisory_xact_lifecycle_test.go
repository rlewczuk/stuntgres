package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAdvisoryXactLifecycleSavepoints(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		for _, k := range advisorySessionKeys {
			for _, prepared := range []bool{false, true} {
				q := k.call("pg_advisory_xact_lock")
				var x fixture.Executor = a
				if prepared {
					x = advisoryXactHeld(t, p, a, q)
				}
				advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
				advisoryXactProbe(t, p, peer, k, false, true)
				for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
					advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
					advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
					advisoryXactProbe(t, p, peer, k, false, false)
					advisorySessionExec(t, p.Ctx, a, end, 0)
					advisoryXactProbe(t, p, peer, k, false, true)
					if end == "COMMIT AND CHAIN" || end == "ROLLBACK AND CHAIN" {
						advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
					}
				}
				for _, outerShared := range []bool{false, true} {
					advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
					advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(true, outerShared, false)), "VOID", "")
					advisorySessionExec(t, p.Ctx, a, "SAVEPOINT child", 0)
					advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(true, !outerShared, false)), "VOID", "")
					advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
					advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO child", 0)
					advisoryXactProbe(t, p, peer, k, true, outerShared)
					advisoryXactProbe(t, p, peer, k, false, false)
					// A repeat of the outer mode in the child does not discard the old hold.
					advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(true, outerShared, false)), "VOID", "")
					advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO child", 0)
					advisoryXactProbe(t, p, peer, k, true, outerShared)
					advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				}
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				advisorySessionExec(t, p.Ctx, a, "SAVEPOINT parent", 0)
				advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
				advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "SAVEPOINT nested", 0)
				other := advisorySessionKeys[2-k.space]
				advisorySessionValue(t, p.Ctx, a, other.call("pg_advisory_xact_lock_shared"), "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "RELEASE nested", 0)
				advisoryXactProbe(t, p, peer, other, false, false)
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO parent", 0)
				advisoryXactProbe(t, p, peer, k, false, true)
				advisoryXactProbe(t, p, peer, other, false, true)
				advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO parent", 0)
				advisoryXactProbe(t, p, peer, k, false, true)
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				// Earlier and later session contributions survive savepoint/full rollback.
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "SAVEPOINT s", 0)
				advisorySessionValue(t, p.Ctx, a, other.call("pg_advisory_lock"), "VOID", "")
				advisorySessionValue(t, p.Ctx, x, q, "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO s", 0)
				advisoryXactProbe(t, p, peer, k, true, true)
				advisoryXactProbe(t, p, peer, k, false, false)
				advisoryXactProbe(t, p, peer, other, false, false)
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				advisoryXactProbe(t, p, peer, k, false, false)
				advisoryXactProbe(t, p, peer, other, false, false)
				advisorySessionClear(t, p.Ctx, a)
			}
		}
		var notices []fixture.Diagnostic
		pool, borrower, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		for _, k := range advisorySessionKeys {
			for _, shared := range []bool{false, true} {
				advisorySessionExec(t, p.Ctx, borrower, "BEGIN", 0)
				advisorySessionValue(t, p.Ctx, borrower, k.call(advisoryXactName(true, shared, false)), "VOID", "")
				unlock, mode := "pg_advisory_unlock", "ExclusiveLock"
				if shared {
					unlock += "_shared"
					mode = "ShareLock"
				}
				s := fixture.Select(k.call(unlock), []string{unlock}, []string{"BOOL"}, [][]any{{false}})
				before := len(notices)
				fixture.RunStep(t, p.Ctx, borrower, record, s)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "you don't own a lock of type " + mode}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal("transaction unlock warning", notices)
				}
				advisorySessionClear(t, p.Ctx, borrower)
				advisoryXactProbe(t, p, peer, k, false, false)
				advisorySessionExec(t, p.Ctx, borrower, "ROLLBACK", 0)
			}
		}
		q := "SELECT pg_advisory_xact_lock(71)"
		stmt, err := borrower.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		advisorySessionExec(t, p.Ctx, borrower, "BEGIN", 0)
		advisorySessionValue(t, p.Ctx, fixture.HeldStatement{Executor: borrower, Statement: stmt, SQL: q}, q, "VOID", "")
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		var physical any
		if err := borrower.Raw(func(raw any) error { physical = raw; return nil }); err != nil {
			t.Fatal(err)
		}
		if err := borrower.Close(); err != nil {
			t.Fatal(err)
		}
		key := advisorySessionKey{"71", 0, 71, 1}
		advisoryXactProbe(t, p, peer, key, false, false)
		reused, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reused.Close() })
		if err := reused.Raw(func(raw any) error {
			if raw != physical {
				t.Fatal("physical session changed")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		advisoryXactProbe(t, p, peer, key, false, false)
		pool.SetMaxIdleConns(0)
		if err := reused.Close(); err != nil {
			t.Fatal(err)
		}
		advisoryXactProbe(t, p, peer, key, false, true)
		advisoryXactControls(t, p, b)
	})
}
