package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionConcurrency(t *testing.T) {
	for _, finish := range []string{"COMMIT", "ROLLBACK", "cancel"} {
		partitionExclusionCase(t, finish, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			run := func(x fixture.Executor, s fixture.Step) {
				s.Prepared = prepared
				fixture.ExclusionReferenceRun(t, p, b, x, s)
			}
			partitionExclusionTree(func(s fixture.Step) { run(a, s) }, "")
			run(a, fixture.Exec("BEGIN", 0))
			run(a, fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
			// Same leaf, different tenant must not wait for the owner's outcome.
			run(peer, fixture.Exec("INSERT INTO pe_root VALUES(9,'10.0.0.2','[3,7)')", 1))
			query := "INSERT INTO pe_root VALUES(2,'10.0.0.129','[1,5)'),(3,'10.0.0.1','[3,7)')"
			var pid, owner int
			if b.PG {
				pid, owner = fixture.LockPID(t, p.Ctx, peer), fixture.LockPID(t, p.Ctx, a)
			} else {
				// Public native timeout proves admission reached a real wait before
				// the owner can complete; no sleep or worker-order inference.
				run(peer, fixture.Exec("SET lock_timeout='50ms'", 0))
				run(peer, referenceRangeError(query, "55P03", "canceling statement due to lock timeout", fixture.Diagnostic{}))
				run(peer, fixture.Exec("SET lock_timeout=0", 0))
			}
			if finish == "cancel" && !b.PG {
				run(peer, fixture.Exec("SET statement_timeout='50ms'", 0))
				run(peer, referenceRangeError(query, "57014", "canceling statement due to statement timeout", fixture.Diagnostic{}))
				run(peer, fixture.Exec("SET statement_timeout=0", 0))
			} else {
				done := fixture.RowAsync(t, p.Ctx, peer, query, prepared, false)
				if b.PG {
					fixture.RowWait(t, p.Ctx, pid, owner)
				}
				if finish == "cancel" {
					var acknowledged bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&acknowledged); err != nil || !acknowledged {
						t.Fatal("cancel acknowledgement", acknowledged, err)
					}
				} else {
					run(a, fixture.Exec(finish, 0))
				}
				out := fixture.LockResult(t, done)
				switch finish {
				case "COMMIT":
					want := fixture.ExclusionReferenceConflict("pe_low", "pe_low_tenant_during_excl", "tenant, during", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")
					fixture.AssertDiagnostic(t, out.Err, &want)
				case "ROLLBACK":
					fixture.State(t, out.Err, "")
					fixture.LockAffected(t, out.Result, 2)
				case "cancel":
					fixture.ExclusionReferenceWaitDiagnostic(t, out.Err, "57014", "canceling statement due to user request", "pe_low", true)
					if out.Result != nil {
						t.Fatal("canceled write returned a result")
					}
				}
			}
			if finish == "cancel" {
				run(a, fixture.Exec("ROLLBACK", 0))
				run(peer, partitionExclusionIDs(9))
				run(peer, fixture.Exec(query, 2))
			}
			if finish == "COMMIT" {
				run(peer, partitionExclusionIDs(1, 9))
			} else {
				run(peer, partitionExclusionIDs(2, 3, 9))
			}
			run(peer, partitionExclusionGraph())
		})
	}
}
