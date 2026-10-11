package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDriverBatchRoleRetirementPeers(t *testing.T) {
	for _, kind := range []string{"schema", "policy"} {
		for _, disposition := range []string{"commit", "rollback", "prepared_commit", "prepared_rollback", "savepoint_commit"} {
			t.Run(kind+"/"+disposition, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"batch_retired"})
				fixture.SchemaReferenceCleanup(t, "batch_retirement_peer_schema")
				batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
					batchSeed(t, p, b, f.A)
					batchPolicySeed(t, p, b, f.A)
					batchExec(t, p, b, f.A, "CREATE ROLE batch_retired LOGIN", 0)
					var oid int64
					if err := f.A.QueryRowContext(p.Ctx, "SELECT usesysid FROM pg_user WHERE usename='batch_retired'").Scan(&oid); err != nil || oid <= 0 || oid > 1<<32-1 {
						t.Fatal("role identity before retirement", oid, err)
					}
					prepared := strings.HasPrefix(disposition, "prepared_")
					gid := "batch_retirement_" + kind
					q := "INSERT INTO batch_t VALUES(8); DROP ROLE batch_retired; BEGIN"
					if prepared {
						q += "; PREPARE TRANSACTION '" + gid + "'"
					} else if disposition == "savepoint_commit" {
						q += "; SAVEPOINT retained; INSERT INTO batch_t VALUES(9); ROLLBACK TO retained"
					}
					batchExec(t, p, b, f.A, q, 0)
					var preparedBefore any
					if prepared {
						preparedBefore = fixture.PreparedReferenceOne(t, p, f.B, gid)
					}
					peerSQL := "CREATE SCHEMA batch_retirement_peer_schema AUTHORIZATION batch_retired"
					if kind == "policy" {
						peerSQL = "CREATE POLICY retirement_peer ON policy_t TO batch_retired USING(id>0) WITH CHECK(allowed)"
					}
					ownerPID, peerPID := 0, 0
					if b.PG {
						peerPID = fixture.LockPID(t, p.Ctx, f.B)
						if !prepared {
							ownerPID = fixture.LockPID(t, p.Ctx, f.A)
						}
					}
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						return fixture.RowExecute(ctx, f.B, peerSQL, false)
					})
					// Cleanup releases the retained role lock before joining a blocked
					// worker if any assertion fails. Prepared blockers have PID zero.
					t.Cleanup(func() {
						if prepared {
							_, _ = f.A.ExecContext(p.Ctx, "ROLLBACK PREPARED '"+gid+"'")
						} else {
							_, _ = f.A.ExecContext(p.Ctx, "ROLLBACK")
						}
					})
					var peer fixture.RelationOutcome
					before := ""
					if b.PG {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						fixture.RowWait(t, ctx, peerPID, ownerPID)
					} else {
						peer = join()
						if peer.Err != nil {
							t.Fatal("native peer must commit before optimistic final preflight", peer.Err)
						}
						fixture.LockAffected(t, peer.Result, 0)
						before = batchPolicyDump(t, p)
					}
					commit := disposition != "rollback" && disposition != "prepared_rollback"
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					if prepared {
						end += " PREPARED '" + gid + "'"
					}
					if commit && !b.PG {
						batchRetirementReject(t, p, f.A, end)
						if after := batchPolicyDump(t, p); after != before {
							t.Fatal("failed retirement changed the committed peer catalog", before, after)
						}
						if prepared {
							if after := fixture.PreparedReferenceOne(t, p, f.B, gid); !reflect.DeepEqual(preparedBefore, after) {
								t.Fatal("failed prepared completion consumed or changed its registry entry", preparedBefore, after)
							}
							batchExec(t, p, b, f.A, "ROLLBACK PREPARED '"+gid+"'", 0)
						}
					} else {
						batchExec(t, p, b, f.A, end, 0)
					}
					if b.PG {
						peer = join()
						if commit {
							if peer.Result != nil || peer.Err == nil {
								t.Fatal("peer command must fail after real role-lock wait and committed retirement", peer.Result, peer.Err)
							}
							want := batchDiag("42704", fmt.Sprintf("role %d was concurrently dropped", oid))
							fixture.AssertDiagnostic(t, peer.Err, &want)
						} else {
							if peer.Err != nil {
								t.Fatal(peer.Err)
							}
							fixture.LockAffected(t, peer.Result, 0)
						}
					}
					if prepared {
						fixture.PreparedReferenceAbsent(t, p, f.B, gid)
					}
					present := !b.PG || !commit
					batchRetirementRole(t, p, b, f.B, present)
					batchRetirementPeer(t, p, b, f, kind, present)
					if b.PG && commit {
						batchValues(t, p, b, f.A, "SELECT i FROM batch_t ORDER BY i", 7, 8)
					} else {
						batchValues(t, p, b, f.A, "SELECT i FROM batch_t ORDER BY i", 7)
					}
					f.NoticesWant(t, "", "")
				})
			})
		}
	}
}
