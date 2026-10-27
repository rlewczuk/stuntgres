package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRLSPolicyLifecycle(t *testing.T) {
	policyTransactionWitnesses(t)
	t.Run("retained_rows", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE POLICY p ON t USING(id>0)")
			rows, err := s.Backend.DB.QueryContext(s.P.Ctx, "SELECT id,allowed FROM t ORDER BY id")
			if err != nil {
				s.T.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 2 || columns[0] != "id" || columns[1] != "allowed" {
				s.T.Fatal(columns, err)
			}
			s.Exec("ALTER POLICY p ON t USING(FALSE)")
			s.Exec("DROP POLICY p ON t")
			if !s.Backend.PG {
				dump := policyDump(s)
				if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
					s.T.Fatal(err)
				}
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump)); err != nil {
					s.T.Fatal(err)
				}
			}
			for _, want := range []struct {
				id      int
				allowed bool
			}{{1, true}, {2, false}} {
				if !rows.Next() {
					s.T.Fatal("retained row missing", rows.Err())
				}
				var id int
				var allowed bool
				if err := rows.Scan(&id, &allowed); err != nil || id != want.id || allowed != want.allowed {
					s.T.Fatal("retained row", id, allowed, err)
				}
			}
			if rows.Next() || rows.Err() != nil {
				s.T.Fatal("retained rows tail", rows.Err())
			}
			rows.Close()
			policyRows(s)
		})
	})
	t.Run("temporary_and_noninheritance", func(t *testing.T) {
		fixture.RoleMembershipCases(t, []string{"policy_temp_target"}, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE POLICY p ON t TO policy_temp_target USING(id>0)")
			s.Exec("CREATE TABLE child() INHERITS(t)")
			policyError(s, "ALTER POLICY p ON child", "42704", `policy "p" for table "child" does not exist`, "", 0)
			s.Exec("CREATE TEMP TABLE policy_temp(id INTEGER)")
			s.Exec("CREATE POLICY temp_policy ON policy_temp TO policy_temp_target USING(id>0)")
			s.Run(fixture.Exec("INSERT INTO policy_temp VALUES(7)", 1))
			if !s.Backend.PG {
				dump := policyDump(s)
				if strings.Contains(dump, "temp_policy") {
					s.T.Fatal("TEMP policy exported", dump)
				}
				if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
					s.T.Fatal(err)
				}
				s.Exec("ALTER POLICY temp_policy ON policy_temp USING(TRUE)")
				policyError(s, "DROP ROLE policy_temp_target", "2BP01", `role "policy_temp_target" cannot be dropped because some objects depend on it`, "target of policy temp_policy on table policy_temp", 0)
			} else {
				s.Exec("DROP TABLE t CASCADE")
				s.ClearNotices()
			}
			s.Run(fixture.Select("SELECT id FROM policy_temp", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			s.Exec("DROP TABLE policy_temp")
			s.Exec("DROP ROLE policy_temp_target")
		})
	})
	t.Run("partitioned_target", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			s.Exec("CREATE TABLE policy_partition(id INTEGER) PARTITION BY RANGE(id)")
			s.Exec("CREATE TABLE policy_leaf PARTITION OF policy_partition FOR VALUES FROM(0) TO(10)")
			s.Exec("CREATE POLICY p ON policy_partition USING(id>0)")
			policyError(s, "ALTER POLICY p ON policy_leaf", "42704", `policy "p" for table "policy_leaf" does not exist`, "", 0)
			s.Exec("ALTER POLICY p ON policy_partition USING(TRUE)")
			s.Exec("DROP POLICY p ON policy_partition")
		})
	})
	for _, operation := range []string{"cancel", "owner_revoked"} {
		t.Run(operation, func(t *testing.T) {
			fixture.RoleMembershipCases(t, []string{"policy_owner", "policy_member"}, func(s *fixture.RoleMembershipScenario) {
				s.Actor("policy_owner")
				fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "policy_owner", func() { policySetup(s) })
				s.Exec("CREATE POLICY p ON t USING(id>0)")
				s.Actor("")
				s.Exec("GRANT policy_owner TO policy_member WITH INHERIT TRUE")
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
				s.Actor("policy_member")
				q := "ALTER POLICY p ON t USING(FALSE)"
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
						// Cancel on the server: pq context cancellation discards the
						// physical session needed for the post-failure catalog probes.
						var canceled bool
						if err := fixture.Admin.QueryRowContext(s.P.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							s.T.Fatal("server cancellation", canceled, err)
						}
					} else {
						cancel()
					}
				} else {
					if _, err := s.Backend.DB.ExecContext(s.P.Ctx, "REVOKE INHERIT OPTION FOR policy_owner FROM policy_member"); err != nil {
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
				s.Actor("")
				policyColumnBlocked(s, "id", "p")
				policyRows(s)
			})
		})
	}
}

func policyLockExecute(ctx context.Context, c *sql.Conn, stmt *sql.Stmt, q string, done chan<- error) {
	var err error
	if stmt != nil {
		_, err = stmt.ExecContext(ctx)
	} else {
		_, err = c.ExecContext(ctx, q)
	}
	done <- err
}

func policyWaitNative(t *testing.T, ctx context.Context, done chan error) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	for ctx.Err() == nil {
		select {
		case err := <-done:
			done <- err
			t.Fatalf("policy operation completed before relation wait: %v", err)
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			wait, worker := false, false
			for {
				frame, more := frames.Next()
				wait = wait || strings.Contains(frame.Function, "(*relationRequest).wait")
				worker = worker || strings.HasSuffix(frame.Function, ".policyLockExecute")
				if !more {
					break
				}
			}
			if wait && worker {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatal(ctx.Err())
}
