package reference_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCommentTimeouts(t *testing.T) {
	for _, timer := range []struct{ setting, code, message string }{
		{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
		{"statement_timeout", "57014", "canceling statement due to statement timeout"},
	} {
		for _, held := range []bool{false, true} {
			t.Run(timer.setting+"/held="+map[bool]string{false: "false", true: "true"}[held], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_comment_timeout(a INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "COMMENT ON TABLE tx_comment_timeout IS 'old'", 0)
						q := "COMMENT ON TABLE tx_comment_timeout IS 'bad'"
						s, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						fixture.LockExec(t, p.Ctx, a, "COMMENT ON TABLE tx_comment_timeout IS 'private'", 0)
						fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
						fixture.LockExec(t, p.Ctx, c, "SET "+timer.setting+"='250ms'", 0)
						defer c.Raw(func(any) error { return driver.ErrBadConn })
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							var o fixture.RelationOutcome
							if held {
								o.Result, o.Err = s.ExecContext(ctx)
							} else {
								o.Result, o.Err = c.ExecContext(ctx, q)
							}
							return o
						})
						// Exact timeout diagnostics establish the completed timed wait.
						// Strict queue admission is independently observed without a
						// short timer in TestReferenceTransactionalCommentLocks.
						o := join()
						fixture.WaitsDiagnostic(t, o.Err, timer.code, timer.message)
						// The armed physical owner is disposed, never RESET or retried.
						_ = c.Raw(func(any) error { return driver.ErrBadConn })
						fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT obj_description('tx_comment_timeout'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{"old"}}))
						fixture.LockExec(t, p.Ctx, a, "COMMENT ON TABLE tx_comment_timeout IS 'follower'", 0)
					})
				}
			})
		}
	}
}
