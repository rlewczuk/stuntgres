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

func combinationWait(s *membershipScenario, ctx context.Context, done <-chan fixture.RelationOutcome, holder, worker int, joined *bool) {
	s.t.Helper()
	if s.backend.PG {
		fixture.RowWait(s.t, ctx, worker, holder)
		return
	}
	// Observe the real parked request, never middleware entry or elapsed time.
	for ctx.Err() == nil {
		select {
		case out := <-done:
			*joined = true
			s.t.Fatalf("worker completed before row wait: %v", out.Err)
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
	s.t.Fatal("row wait not observed", ctx.Err())
}
func combinationMutation(ctx context.Context, s *membershipScenario, q string, done chan<- fixture.RelationOutcome) {
	done <- fixture.RowExecute(ctx, s.c, q, s.prepared)
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
				combinationCases(t, func(s *membershipScenario) {
					s.exec("CREATE POLICY p ON t USING(allowed) WITH CHECK(TRUE)")
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
					if _, err := tx.ExecContext(s.p.Ctx, "UPDATE t SET allowed=FALSE,v=0 WHERE id=1"); err != nil {
						s.t.Fatal(err)
					}
					hp, wp := 0, 0
					if s.backend.PG {
						hp = fixture.LockPID(s.t, s.p.Ctx, holder)
						wp = fixture.LockPID(s.t, s.p.Ctx, s.c)
					}
					s.exec("SET ROLE comb_reader")
					ctx, cancel := context.WithTimeout(s.p.Ctx, 5*time.Second)
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
								s.t.Error("worker failed to join")
							}
						}
					}()
					go combinationMutation(ctx, s, q, done)
					combinationWait(s, ctx, done, hp, wp, &joined)
					if cancelled {
						if s.backend.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", wp).Scan(&ok); err != nil || !ok {
								s.t.Fatal(ok, err)
							}
						} else {
							cancel()
						}
					} else if err := tx.Commit(); err != nil {
						s.t.Fatal(err)
					}
					out := fixture.LockResult(s.t, done)
					joined = true
					if cancelled {
						if s.backend.PG {
							operation := "updating"
							if strings.HasPrefix(q, "DELETE") {
								operation = "deleting"
							}
							fixture.AssertDiagnostic(s.t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while " + operation + " tuple (0,1) in relation \"t\""})
						} else if !errors.Is(out.Err, context.Canceled) {
							s.t.Fatal(out.Err)
						}
						if out.Result != nil {
							s.t.Fatal("cancel published result")
						}
						tx.Rollback()
					} else {
						if out.Err != nil {
							s.t.Fatal(out.Err)
						}
						fixture.LockAffected(s.t, out.Result, 0)
					}
					s.exec("RESET ROLE")
					value := int64(0)
					allowed := false
					if cancelled {
						value = 10
						allowed = true
					}
					s.run(fixture.Select("SELECT allowed,v FROM t WHERE id=1", []string{"allowed", "v"}, []string{"BOOL", "INT4"}, [][]any{{allowed, value}}))
				})
			})
		}
	}
}
