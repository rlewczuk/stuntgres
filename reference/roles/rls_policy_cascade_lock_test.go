package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// RemovePolicyById in PostgreSQL REL_18_0 policy.c acquires AccessExclusive
// on the surviving owner even when dependency traversal initiated the removal.
// Only t is locked here: neither provider nor dependent_view can account for
// the worker's wait. Native uses the existing actual relation-wait stack barrier;
// PostgreSQL additionally identifies t, the requested mode and the exact blocker.
func TestReferenceRLSPolicyCascadeOwnerLock(t *testing.T) {
	for _, kind := range []string{"domain", "mixed_view_policy"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleMembershipCases(t, []string{"policy_cascade_target"}, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				q := "DROP DOMAIN policy_domain CASCADE"
				restrict := "DROP DOMAIN policy_domain"
				message := "cannot drop type policy_domain because other objects depend on it"
				detail := "policy p on table t depends on type policy_domain"
				notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy p on table t"}
				if kind == "domain" {
					s.Exec("CREATE DOMAIN policy_domain AS INTEGER")
					s.Exec("CREATE POLICY p ON t TO policy_cascade_target USING(id::policy_domain>0) WITH CHECK(allowed)")
				} else {
					q = "DROP TABLE helper CASCADE"
					restrict = "DROP TABLE helper"
					message = "cannot drop table helper because other objects depend on it"
					detail = "view dependent_view depends on table helper\npolicy p on table t depends on table helper"
					notice.Message = "drop cascades to 2 other objects"
					notice.Detail = "drop cascades to view dependent_view\ndrop cascades to policy p on table t"
					s.Exec("CREATE TABLE helper(id INTEGER)")
					s.Run(fixture.Exec("INSERT INTO helper VALUES(7),(9)", 2))
					s.Exec("CREATE VIEW dependent_view AS SELECT id FROM helper")
					s.Exec("CREATE POLICY p ON t TO policy_cascade_target USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)) WITH CHECK(allowed)")
				}
				policyRestrict(s, restrict, message, detail)

				holder, err := s.Backend.DB.Conn(s.P.Ctx)
				if err != nil {
					s.T.Fatal(err)
				}
				defer holder.Close()
				var stmt *sql.Stmt
				if s.Prepared {
					stmt, err = s.C.PrepareContext(s.P.Ctx, q)
					if err != nil {
						s.T.Fatal(err)
					}
					defer stmt.Close()
				}
				workerPID, holderPID := 0, 0
				var ownerOID int64
				if s.Backend.PG {
					workerPID = fixture.LockPID(s.T, s.P.Ctx, s.C)
					holderPID = fixture.LockPID(s.T, s.P.Ctx, holder)
					ownerOID = fixture.LockOID(s.T, s.P.Ctx, "public.t")
				}

				for _, canceled := range []bool{true, false} {
					func() {
						tx, err := holder.BeginTx(s.P.Ctx, nil)
						if err != nil {
							s.T.Fatal(err)
						}
						defer tx.Rollback()
						if _, err := tx.ExecContext(s.P.Ctx, "LOCK TABLE t IN ACCESS SHARE MODE"); err != nil {
							s.T.Fatal(err)
						}
						ctx, cancel := context.WithTimeout(s.P.Ctx, 5*time.Second)
						defer cancel()
						done := make(chan error, 1)
						joined := false
						defer func() {
							cancel()
							tx.Rollback()
							if !joined {
								<-done
							}
						}()
						go policyLockExecute(ctx, s.C, stmt, q, done)
						if s.Backend.PG {
							fixture.LockWaitBehind(s.T, ctx, workerPID, holderPID, ownerOID, "AccessExclusiveLock")
						} else {
							policyWaitNative(s.T, ctx, done)
						}
						if canceled {
							if s.Backend.PG {
								// Server cancellation retains the pq physical connection for
								// the independent post-failure probes and prepared retry.
								var signaled bool
								if err := fixture.Admin.QueryRowContext(s.P.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&signaled); err != nil || !signaled {
									s.T.Fatal("server cancellation", signaled, err)
								}
							} else {
								cancel()
							}
						} else if err := tx.Commit(); err != nil {
							s.T.Fatal(err)
						}
						err = <-done
						joined = true
						if canceled {
							if s.Backend.PG {
								fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
							} else if !errors.Is(err, context.Canceled) {
								s.T.Fatalf("cascade cancellation: %v", err)
							}
						} else if err != nil {
							s.T.Fatal("cascade after owner lock release", err)
						}
					}()
					if canceled {
						// PostgreSQL may report the planned closure before deletion;
						// such notices do not prove publication. Probe actual survivors.
						s.ClearNotices()
						policyRestrict(s, restrict, message, detail)
						policyRoleBlocked(s, "policy_cascade_target", "p")
						policyColumnBlocked(s, "id", "p")
						policyColumnBlocked(s, "allowed", "p")
						policyRows(s)
						if kind == "domain" {
							s.Run(fixture.Select("SELECT 7::policy_domain AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(7)}}))
						} else {
							for _, source := range []string{"helper", "dependent_view"} {
								s.Run(fixture.Select("SELECT id FROM "+source+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
							}
						}
					} else {
						policyNotice(s, notice)
					}
				}
				policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
				s.Exec("DROP ROLE policy_cascade_target")
				policyRows(s)
				// Same-name recreation independently proves retirement, not merely
				// removal of the policy edge; replacement providers have no blocker.
				if kind == "domain" {
					s.Exec("CREATE DOMAIN policy_domain AS INTEGER")
					s.Exec("DROP DOMAIN policy_domain")
				} else {
					s.Exec("CREATE TABLE helper(id INTEGER)")
					s.Exec("CREATE VIEW dependent_view AS SELECT id FROM helper")
					s.Run(fixture.Select("SELECT id FROM dependent_view", []string{"id"}, []string{"INT4"}, nil))
					s.Exec("DROP VIEW dependent_view")
					s.Exec("DROP TABLE helper")
				}
			})
		})
	}
}
