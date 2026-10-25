package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCommentLocks(t *testing.T) {
	for _, mode := range []string{"SHARE UPDATE EXCLUSIVE", "ACCESS EXCLUSIVE"} {
		for _, held := range []bool{false, true} {
			t.Run(mode+"/held="+map[bool]string{false: "false", true: "true"}[held], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_comment_lock(a INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "COMMENT ON TABLE tx_comment_lock IS 'old'", 0)
						q := "COMMENT ON TABLE tx_comment_lock IS 'new'"
						s, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						if !b.PG {
							for _, expired := range []bool{false, true} {
								canceled, cancel := context.WithCancel(p.Ctx)
								want := error(context.Canceled)
								if expired {
									cancel()
									canceled, cancel = context.WithTimeout(p.Ctx, 0)
									want = context.DeadlineExceeded
								}
								cancel()
								_, err := s.ExecContext(canceled)
								if !errors.Is(err, want) {
									t.Fatal("pre-canceled/expired comment", err)
								}
								var state interface{ SQLState() string }
								if errors.As(err, &state) {
									t.Fatal("ordinary context error acquired SQLSTATE", err)
								}
							}
						}
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						fixture.LockExec(t, p.Ctx, a, "LOCK TABLE tx_comment_lock IN "+mode+" MODE", 0)
						if mode == "SHARE UPDATE EXCLUSIVE" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a FROM tx_comment_lock", []string{"a"}, []string{"INT4"}, [][]any{}))
							fixture.LockExec(t, p.Ctx, c, "INSERT INTO tx_comment_lock VALUES(7)", 1)
						}
						fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
							var o fixture.RelationOutcome
							if held {
								o.Result, o.Err = s.ExecContext(ctx)
							} else {
								o.Result, o.Err = c.ExecContext(ctx, q)
							}
							return o
						})
						fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT obj_description('tx_comment_lock'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{"old"}}))
						fixture.LockExec(t, p.Ctx, c, q, 0)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT obj_description('tx_comment_lock'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{"new"}}))
						for _, ending := range []string{"COMMIT", "ROLLBACK"} {
							fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
							fixture.LockExec(t, p.Ctx, a, q, 0)
							fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("LOCK TABLE tx_comment_lock IN SHARE UPDATE EXCLUSIVE MODE NOWAIT", fixture.Diagnostic{Code: "55P03", Message: `could not obtain lock on relation "tx_comment_lock"`}))
							fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
							fixture.LockExec(t, p.Ctx, a, ending, 0)
							fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
							fixture.LockExec(t, p.Ctx, c, "LOCK TABLE tx_comment_lock IN SHARE UPDATE EXCLUSIVE MODE NOWAIT", 0)
							fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
						}
					})
				}
			})
		}
	}
	t.Run("savepoint-contributions", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				for _, q := range []string{"CREATE TABLE older(a INT)", "CREATE TABLE newer(a INT)", "BEGIN", "COMMENT ON TABLE older IS 'older'", "SAVEPOINT mark", "COMMENT ON TABLE newer IS 'newer'", "ROLLBACK TO mark"} {
					fixture.LockExec(t, p.Ctx, a, q, 0)
				}
				defer a.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, c, "LOCK TABLE newer IN SHARE UPDATE EXCLUSIVE MODE NOWAIT", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("LOCK TABLE older IN SHARE UPDATE EXCLUSIVE MODE NOWAIT", fixture.Diagnostic{Code: "55P03", Message: `could not obtain lock on relation "older"`}))
				fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, a, "COMMENT ON TABLE newer IS 'released'", 0)
				fixture.LockExec(t, p.Ctx, a, "RELEASE mark", 0)
				fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("LOCK TABLE newer IN SHARE UPDATE EXCLUSIVE MODE NOWAIT", fixture.Diagnostic{Code: "55P03", Message: `could not obtain lock on relation "newer"`}))
				fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
				fixture.LockExec(t, p.Ctx, c, "COMMENT ON TABLE older IS 'follower'", 0)
				fixture.LockExec(t, p.Ctx, c, "COMMENT ON TABLE newer IS 'follower'", 0)
			})
		}
	})
	t.Run("captured-replacement", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_comment_lock(a INT)", 0)
				q := "COMMENT ON TABLE tx_comment_lock IS 'stale'"
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				pid, holder := 0, 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
					holder = fixture.LockPID(t, p.Ctx, a)
				}
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				defer a.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, a, "LOCK TABLE tx_comment_lock IN ACCESS EXCLUSIVE MODE", 0)
				done := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					r, err := s.ExecContext(ctx)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				if b.PG {
					fixture.LockWaitBehind(t, p.Ctx, pid, holder, fixture.LockOID(t, p.Ctx, "tx_comment_lock"), "ShareUpdateExclusiveLock")
				} else {
					fixture.NativeRelationLockWait(t, p.Ctx, done)
				}
				fixture.LockExec(t, p.Ctx, a, "DROP TABLE tx_comment_lock", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_comment_lock(a INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
				o := join()
				var want any
				if b.PG {
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					want = "stale"
				} else {
					fixture.TransactionOrdinary(t, o.Err, "stuntgres: catalog replaced before publication")
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT obj_description('tx_comment_lock'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{want}}))
				fixture.LockExec(t, p.Ctx, c, "COMMENT ON TABLE tx_comment_lock IS 'fresh'", 0)
			})
		}
	})
}
