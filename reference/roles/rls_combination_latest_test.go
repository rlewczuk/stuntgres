package reference_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func combinationWait(s *fixture.RoleMembershipScenario, ctx context.Context, done <-chan fixture.RelationOutcome, holder, worker int, joined *bool) {
	s.T.Helper()
	if s.Backend.PG {
		fixture.RowWait(s.T, ctx, worker, holder)
		return
	}
	// Observe the real parked request, never middleware entry or elapsed time.
	for ctx.Err() == nil {
		select {
		case out := <-done:
			*joined = true
			s.T.Fatalf("worker completed before row wait: %v", out.Err)
		default:
		}
		size := 64 * 1024
		for {
			stacks := make([]byte, size)
			n := runtime.Stack(stacks, true)
			if n == len(stacks) {
				size *= 2
				continue
			}
			for _, stack := range strings.Split(string(stacks[:n]), "\n\n") {
				if strings.Contains(stack, ".(*relationRequest).wait(") && strings.Contains(stack, "combinationMutation(") {
					return
				}
			}
			break
		}
		runtime.Gosched()
	}
	s.T.Fatal("row wait not observed", ctx.Err())
}
func combinationMutation(ctx context.Context, s *fixture.RoleMembershipScenario, q string, done chan<- fixture.RelationOutcome) {
	done <- fixture.RowExecute(ctx, s.C, q, s.Prepared)
}
func TestReferenceRLSCombinationLatestRow(t *testing.T) {
	for _, q := range []string{
		"UPDATE t SET v=100/v WHERE id=1",
		"UPDATE t SET v=100/t.v FROM (SELECT 1 AS id) x WHERE t.id=x.id",
		"DELETE FROM t WHERE id=1 AND 100/v>0",
		"DELETE FROM t USING (SELECT 1 AS id) x WHERE t.id=x.id AND 100/t.v>0",
	} {
		for _, cancelled := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/refresh", true: "/cancel"}[cancelled], func(t *testing.T) {
				combinationCases(t, func(s *fixture.RoleMembershipScenario) {
					s.Exec("CREATE POLICY p ON t USING(allowed) WITH CHECK(TRUE)")
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
					if _, err := tx.ExecContext(s.P.Ctx, "UPDATE t SET allowed=FALSE,v=0 WHERE id=1"); err != nil {
						s.T.Fatal(err)
					}
					hp, wp := 0, 0
					if s.Backend.PG {
						hp = fixture.LockPID(s.T, s.P.Ctx, holder)
						wp = fixture.LockPID(s.T, s.P.Ctx, s.C)
					}
					s.Exec("SET ROLE comb_reader")
					ctx, cancel := context.WithTimeout(s.P.Ctx, 5*time.Second)
					defer cancel()
					done := make(chan fixture.RelationOutcome, 1)
					joined := false
					defer func() {
						cancel()
						tx.Rollback()
						if !joined {
							select {
							case <-done:
							case <-time.After(5 * time.Second):
								s.T.Error("worker failed to join")
							}
						}
					}()
					go combinationMutation(ctx, s, q, done)
					combinationWait(s, ctx, done, hp, wp, &joined)
					if cancelled {
						if s.Backend.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(s.P.Ctx, "SELECT pg_cancel_backend($1)", wp).Scan(&ok); err != nil || !ok {
								s.T.Fatal(ok, err)
							}
						} else {
							cancel()
						}
					} else if err := tx.Commit(); err != nil {
						s.T.Fatal(err)
					}
					out := fixture.LockResult(s.T, done)
					joined = true
					if cancelled {
						if s.Backend.PG {
							operation := "updating"
							if strings.HasPrefix(q, "DELETE") {
								operation = "deleting"
							}
							fixture.AssertDiagnostic(s.T, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while " + operation + " tuple (0,1) in relation \"t\""})
						} else if !errors.Is(out.Err, context.Canceled) {
							s.T.Fatal(out.Err)
						}
						if out.Result != nil {
							s.T.Fatal("cancel published result")
						}
						tx.Rollback()
					} else {
						if out.Err != nil {
							s.T.Fatal(out.Err)
						}
						fixture.LockAffected(s.T, out.Result, 0)
					}
					s.Exec("RESET ROLE")
					value := int64(0)
					allowed := false
					if cancelled {
						value = 10
						allowed = true
					}
					s.Run(fixture.Select("SELECT allowed,v FROM t WHERE id=1", []string{"allowed", "v"}, []string{"BOOL", "INT4"}, [][]any{{allowed, value}}))
				})
			})
		}
	}
}
