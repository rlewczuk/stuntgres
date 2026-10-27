package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseAuthorityWaits(t *testing.T) {
	for _, operation := range []string{"cancel", "owner_revoked"} {
		t.Run(operation, func(t *testing.T) {
			rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
				s.Actor("")
				s.Exec("GRANT rls_owner TO rls_member WITH INHERIT TRUE")
				holder, err := s.Backend.DB.Conn(s.P.Ctx)
				if err != nil {
					s.T.Fatal(err)
				}
				defer holder.Close()
				tx, err := holder.BeginTx(s.P.Ctx, nil)
				if err != nil {
					s.T.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(s.P.Ctx, "LOCK TABLE t IN ACCESS SHARE MODE"); err != nil {
					s.T.Fatal(err)
				}
				s.Actor("rls_member")
				q := "ALTER TABLE t ENABLE ROW LEVEL SECURITY"
				var stmt *sql.Stmt
				if s.Prepared {
					stmt, err = s.C.PrepareContext(s.P.Ctx, q)
					if err != nil {
						s.T.Fatal(err)
					}
					defer stmt.Close()
				}
				pid := 0
				if s.Backend.PG {
					pid = fixture.LockPID(s.T, s.P.Ctx, s.C)
				}
				ctx, cancel := context.WithCancel(s.P.Ctx)
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
					fixture.LockWait(s.T, s.P.Ctx, pid)
				} else {
					policyWaitNative(s.T, ctx, done)
				}
				if operation == "cancel" {
					if s.Backend.PG {
						var canceled bool
						if err := fixture.Admin.QueryRowContext(s.P.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							s.T.Fatal("server cancellation", canceled, err)
						}
					} else {
						cancel()
					}
				} else {
					if _, err := s.Backend.DB.ExecContext(s.P.Ctx, "REVOKE INHERIT OPTION FOR rls_owner FROM rls_member"); err != nil {
						s.T.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						s.T.Fatal(err)
					}
				}
				err = <-done
				joined = true
				if operation == "owner_revoked" {
					fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"})
				} else if s.Backend.PG {
					fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				} else if !errors.Is(err, context.Canceled) {
					s.T.Fatal(err)
				}
				tx.Rollback()
				s.Actor("rls_owner")
				rlsBaseEnabled(s, false)
				rlsBaseRows(s, rlsBaseSeedRows())
			})
		})
	}
}
