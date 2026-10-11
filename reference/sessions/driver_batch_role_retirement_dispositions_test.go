package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchRoleRetirementDispositions(t *testing.T) {
	for _, disposition := range []string{"commit", "late_failure", "promote_commit", "promote_rollback", "prepared_commit", "prepared_rollback", "savepoint_commit", "ephemeral"} {
		t.Run(disposition, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"batch_retired"})
			fixture.SchemaReferenceCleanup(t, "batch_retirement_old", "batch_retirement_unrelated")
			batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				batchSeed(t, p, b, f.A)
				batchPolicySeed(t, p, b, f.A)
				q := "CREATE ROLE batch_retired LOGIN; DROP ROLE batch_retired; INSERT INTO batch_t VALUES(8)"
				if disposition != "ephemeral" {
					batchExec(t, p, b, f.A, "CREATE ROLE batch_retired LOGIN", 0)
					batchExec(t, p, b, f.A, "CREATE SCHEMA batch_retirement_old AUTHORIZATION batch_retired", 0)
					batchExec(t, p, b, f.A, "CREATE POLICY retirement_old ON policy_t TO batch_retired USING(id>0) WITH CHECK(allowed)", 0)
					batchExec(t, p, b, f.A, "GRANT SELECT(i) ON batch_t TO batch_retired", 0)
					if b.PG {
						batchExec(t, p, b, f.A, "GRANT USAGE ON SCHEMA public TO batch_retired", 0)
					}
					q = "DROP POLICY retirement_old ON policy_t; DROP SCHEMA batch_retirement_old; REVOKE SELECT(i) ON batch_t FROM batch_retired"
					if b.PG {
						q += "; REVOKE USAGE ON SCHEMA public FROM batch_retired"
					}
					q += "; DROP ROLE batch_retired; INSERT INTO batch_t VALUES(8)"
				}
				prepared := strings.HasPrefix(disposition, "prepared_")
				retained := prepared || strings.HasPrefix(disposition, "promote_") || disposition == "savepoint_commit"
				gid := "batch_retirement_disposition"
				if retained {
					q += "; BEGIN"
				}
				if prepared {
					fixture.PreparedReferenceCleanup(t, b, gid)
					q += "; PREPARE TRANSACTION '" + gid + "'"
				} else if disposition == "savepoint_commit" {
					q += "; SAVEPOINT retained; INSERT INTO batch_t VALUES(9); ROLLBACK TO retained; RELEASE retained"
				} else if disposition == "late_failure" {
					q += "; SELECT missing FROM batch_t"
				}
				if disposition == "late_failure" {
					want := batchDiag("42703", `column "missing" does not exist`)
					want.Position = batchPosition(q, "missing")
					batchError(t, p, f.A, q, want)
				} else {
					n := int64(1)
					if retained {
						n = 0
					}
					batchExec(t, p, b, f.A, q, n)
				}
				if retained {
					// The final obligation is targeted: an unrelated peer schema
					// changes the catalog root but cannot forbid valid retirement.
					batchExec(t, p, b, f.B, "CREATE SCHEMA batch_retirement_unrelated", 0)
					if prepared {
						fixture.PreparedReferenceOne(t, p, f.B, gid)
					}
					end := "COMMIT"
					if strings.HasSuffix(disposition, "_rollback") {
						end = "ROLLBACK"
					}
					if prepared {
						end += " PREPARED '" + gid + "'"
					}
					batchExec(t, p, b, f.A, end, 0)
					if prepared {
						fixture.PreparedReferenceAbsent(t, p, f.B, gid)
					}
					batchExec(t, p, b, f.B, "CREATE TABLE batch_retirement_unrelated.guard(i INT)", 0)
					batchExec(t, p, b, f.B, "INSERT INTO batch_retirement_unrelated.guard VALUES(43)", 1)
					batchValues(t, p, b, f.B, "SELECT i FROM batch_retirement_unrelated.guard", 43)
				}
				rollback := disposition == "late_failure" || strings.HasSuffix(disposition, "_rollback")
				batchRetirementRole(t, p, b, f.B, rollback)
				if rollback {
					batchValues(t, p, b, f.A, "SELECT i FROM batch_t ORDER BY i", 7)
					batchExec(t, p, b, f.B, "ALTER POLICY retirement_old ON policy_t", 0)
					batchExec(t, p, b, f.B, "SET ROLE batch_retired", 0)
					batchExec(t, p, b, f.B, "CREATE TABLE batch_retirement_old.owner_guard(i INT)", 0)
					batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", 7)
					batchExec(t, p, b, f.B, "RESET ROLE", 0)
				} else {
					batchValues(t, p, b, f.A, "SELECT i FROM batch_t ORDER BY i", 7, 8)
					batchPolicyMissing(t, p, f.B, "retirement_old")
				}
				batchPolicyRows(t, p, b, f.B)
				f.NoticesWant(t, "", "")
			})
		})
	}
}
