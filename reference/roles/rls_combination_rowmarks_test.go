package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRLSCombinationPolicyRowMarks(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE policy_helper(id INT PRIMARY KEY)")
		s.Run(fixture.Exec("INSERT INTO policy_helper VALUES(1)", 1))
		s.Exec("GRANT SELECT ON policy_helper TO comb_reader")
		condition := func(option string) string {
			return "EXISTS(SELECT 1 FROM policy_helper h WHERE h.id=t.id FOR SHARE" + option + ")"
		}
		s.Exec("CREATE POLICY p ON t USING(" + condition("") + ")")
		s.Exec("SET ROLE comb_reader")
		combinationError(s, "SELECT id FROM t ORDER BY id", "42501", "permission denied for table policy_helper", true)
		s.Exec("RESET ROLE")
		s.Exec("GRANT UPDATE ON policy_helper TO comb_reader")
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1)
		s.Exec("BEGIN READ ONLY")
		readOnly := true
		defer func() {
			if readOnly {
				_, _ = s.C.ExecContext(s.P.Ctx, "ROLLBACK")
			}
		}()
		combinationError(s, "SELECT id FROM t WHERE id=1", "25006", "cannot execute SELECT FOR SHARE in a read-only transaction", true)
		s.Exec("ROLLBACK")
		readOnly = false
		// Both the ordinary target mark and nested policy mark survive together.
		combinationIDs(s, "SELECT t.id FROM policy_helper AS outer_helper JOIN t ON outer_helper.id=t.id", 1)
		s.Exec("BEGIN")
		open := true
		defer func() {
			if open {
				_, _ = s.C.ExecContext(s.P.Ctx, "ROLLBACK")
			}
		}()
		combinationIDs(s, "SELECT id FROM t WHERE id=1 FOR UPDATE", 1)
		fixture.QueryLockNowait(s.T, s.P, s.Backend, peer, "SELECT id FROM policy_helper WHERE id=1 FOR UPDATE NOWAIT", s.Prepared, "policy_helper")
		fixture.QueryLockNowait(s.T, s.P, s.Backend, peer, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", s.Prepared, "t")
		s.Exec("ROLLBACK")
		open = false
		fixture.QueryLockRows(s.T, s.P, s.Backend, peer, "SELECT id FROM policy_helper WHERE id=1 FOR UPDATE NOWAIT", s.Prepared, "id", 1)
		fixture.QueryLockRows(s.T, s.P, s.Backend, peer, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", s.Prepared, "id", 1)
		s.Exec("RESET ROLE")
		for _, option := range []string{" NOWAIT", " SKIP LOCKED"} {
			s.Exec("ALTER POLICY p ON t USING(" + condition(option) + ")")
			holder, err := peer.BeginTx(s.P.Ctx, nil)
			if err != nil {
				s.T.Fatal(err)
			}
			func() {
				defer holder.Rollback()
				fixture.QueryLockRows(s.T, s.P, s.Backend, holder, "SELECT id FROM policy_helper WHERE id=1 FOR UPDATE", false, "id", 1)
				s.Exec("SET ROLE comb_reader")
				if option == " NOWAIT" {
					fixture.QueryLockNowait(s.T, s.P, s.Backend, s.C, "SELECT id FROM t WHERE id=1", s.Prepared, "policy_helper")
				} else {
					combinationIDs(s, "SELECT id FROM t WHERE id=1")
				}
				if err := holder.Rollback(); err != nil {
					s.T.Fatal(err)
				}
				combinationIDs(s, "SELECT id FROM t WHERE id=1", 1)
				s.Exec("RESET ROLE")
			}()
		}
		s.Exec("ALTER POLICY p ON t USING(" + condition("") + ")")
		holder, err := peer.BeginTx(s.P.Ctx, nil)
		if err != nil {
			s.T.Fatal(err)
		}
		defer holder.Rollback()
		fixture.QueryLockRows(s.T, s.P, s.Backend, holder, "SELECT id FROM policy_helper WHERE id=1 FOR UPDATE", false, "id", 1)
		hp, wp := 0, 0
		if s.Backend.PG {
			hp = fixture.LockPID(s.T, s.P.Ctx, peer)
			wp = fixture.LockPID(s.T, s.P.Ctx, s.C)
		}
		s.Exec("SET ROLE comb_reader")
		ctx, cancel := context.WithTimeout(s.P.Ctx, 5*time.Second)
		defer cancel()
		done := make(chan fixture.RelationOutcome, 1)
		joined := false
		defer func() {
			cancel()
			holder.Rollback()
			if !joined {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					s.T.Error("policy-lock worker failed to join")
				}
			}
		}()
		// Exec consumes the complete SELECT, avoiding an unconsumed streaming cursor
		// being mistaken for a completed wait. The worker is the real public route.
		go combinationMutation(ctx, s, "SELECT id FROM t WHERE id=1", done)
		combinationWait(s, ctx, done, hp, wp, &joined)
		if s.Backend.PG {
			var ok bool
			if err := fixture.Admin.QueryRowContext(s.P.Ctx, "SELECT pg_cancel_backend($1)", wp).Scan(&ok); err != nil || !ok {
				s.T.Fatal(ok, err)
			}
		} else {
			cancel()
		}
		out := fixture.LockResult(s.T, done)
		joined = true
		if s.Backend.PG {
			fixture.AssertDiagnostic(s.T, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while locking tuple (0,1) in relation \"policy_helper\""})
		} else if !errors.Is(out.Err, context.Canceled) {
			s.T.Fatal(out.Err)
		}
		if out.Result != nil {
			s.T.Fatal("cancelled policy source published a result")
		}
		if err := holder.Rollback(); err != nil {
			s.T.Fatal(err)
		}
		s.Exec("RESET ROLE")
		combinationIDs(s, "SELECT id FROM policy_helper", 1)
		s.Run(fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
	})
}
