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
			rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
				s.actor("")
				s.exec("GRANT rls_owner TO rls_member WITH INHERIT TRUE")
				holder, err := s.backend.DB.Conn(s.p.Ctx)
				if err != nil {
					s.t.Fatal(err)
				}
				defer holder.Close()
				tx, err := holder.BeginTx(s.p.Ctx, nil)
				if err != nil {
					s.t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(s.p.Ctx, "LOCK TABLE t IN ACCESS SHARE MODE"); err != nil {
					s.t.Fatal(err)
				}
				s.actor("rls_member")
				q := "ALTER TABLE t ENABLE ROW LEVEL SECURITY"
				var stmt *sql.Stmt
				if s.prepared {
					stmt, err = s.c.PrepareContext(s.p.Ctx, q)
					if err != nil {
						s.t.Fatal(err)
					}
					defer stmt.Close()
				}
				pid := 0
				if s.backend.PG {
					pid = fixture.LockPID(s.t, s.p.Ctx, s.c)
				}
				ctx, cancel := context.WithCancel(s.p.Ctx)
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
				go policyLockExecute(ctx, s.c, stmt, q, done)
				if s.backend.PG {
					fixture.LockWait(s.t, s.p.Ctx, pid)
				} else {
					policyWaitNative(s.t, ctx, done)
				}
				if operation == "cancel" {
					if s.backend.PG {
						var canceled bool
						if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							s.t.Fatal("server cancellation", canceled, err)
						}
					} else {
						cancel()
					}
				} else {
					if _, err := s.backend.DB.ExecContext(s.p.Ctx, "REVOKE INHERIT OPTION FOR rls_owner FROM rls_member"); err != nil {
						s.t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						s.t.Fatal(err)
					}
				}
				err = <-done
				joined = true
				if operation == "owner_revoked" {
					fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"})
				} else if s.backend.PG {
					fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				} else if !errors.Is(err, context.Canceled) {
					s.t.Fatal(err)
				}
				tx.Rollback()
				s.actor("rls_owner")
				rlsBaseEnabled(s, false)
				rlsBaseRows(s, rlsBaseSeedRows())
			})
		})
	}
}
