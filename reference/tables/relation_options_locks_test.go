package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRelationOptionsLocksCancellation(t *testing.T) {
	for _, tc := range []struct{ name, holder, query, mode string }{
		{"option_conflict", "ALTER TABLE ro SET(fillfactor=70)", "ALTER TABLE ro SET(fillfactor=80)", "ShareUpdateExclusiveLock"},
		{"user_catalog_reader", "SELECT * FROM ro", "ALTER TABLE ro SET(user_catalog_table=true)", "AccessExclusiveLock"},
		{"user_catalog_reset_reader", "SELECT * FROM ro", "ALTER TABLE ro RESET(user_catalog_table)", "AccessExclusiveLock"},
		{"toast_user_catalog_reader", "SELECT * FROM ro", "ALTER TABLE ro SET(toast.user_catalog_table=true)", "AccessExclusiveLock"},
		{"mixed_strongest", "SELECT * FROM ro", "ALTER TABLE ro SET(fillfactor=80),ADD COLUMN extra INT", "AccessExclusiveLock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				relationOptionsSeed(run)
				before := ""
				if !b.PG {
					before = relationOptionsDump(t, p)
				}
				holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
				defer holder.ExecContext(context.Background(), "ROLLBACK")
				fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
				if tc.holder == "SELECT * FROM ro" {
					s := relationOptionsRows("ro")
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, s)
				} else {
					fixture.RowAffected(t, p.Ctx, holder, tc.holder, prepared, 0)
					// Ordinary SELECT must finish while the option transaction retains
					// its lock. This rejects an accidental ACCESS EXCLUSIVE upgrade.
					run(relationOptionsRows("ro"))
				}
				if b.PG {
					pid, blocker := fixture.LockPID(t, p.Ctx, waiter), fixture.LockPID(t, p.Ctx, holder)
					done := fixture.RowAsync(t, p.Ctx, waiter, tc.query, prepared, false)
					fixture.LockWaitBehind(t, p.Ctx, pid, blocker, fixture.LockOID(t, p.Ctx, "ro"), tc.mode)
					var acknowledged bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&acknowledged); err != nil || !acknowledged {
						t.Fatal(acknowledged, err)
					}
					outcome := fixture.LockResult(t, done)
					want := relationOptionsDiagnostic("57014", "canceling statement due to user request", "")
					fixture.AssertDiagnostic(t, outcome.Err, &want)
					if outcome.Result != nil {
						t.Fatal("canceled ALTER produced result")
					}
				} else {
					fixture.RelationLockActiveCancel(t, p, b, waiter, tc.query, func(ctx context.Context) fixture.RelationOutcome {
						return fixture.RowExecute(ctx, waiter, tc.query, prepared)
					})
				}
				fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
				run(relationOptionsState("ro", "fillfactor=70"))
				run(relationOptionsRows("ro"))
				if !b.PG && relationOptionsDump(t, p) != before {
					t.Fatal("cancellation changed dump")
				}
			})
		})
	}
	t.Run("timeouts", func(t *testing.T) {
		relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
			relationOptionsSeed(run)
			holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
			defer holder.ExecContext(context.Background(), "ROLLBACK")
			fixture.RowAffected(t, p.Ctx, holder, "ALTER TABLE ro SET(fillfactor=70)", prepared, 0)
			for _, tc := range []struct{ setting, code, message string }{
				{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
				{"statement_timeout", "57014", "canceling statement due to statement timeout"},
			} {
				fixture.LockExec(t, p.Ctx, waiter, "SET "+tc.setting+"='50ms'", 0)
				s := relationOptionsError("ALTER TABLE ro SET(fillfactor=80)", tc.code, tc.message, "")
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, waiter, b.Recorder, s)
				fixture.LockExec(t, p.Ctx, waiter, "SET "+tc.setting+"=0", 0)
			}
			fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
			run(relationOptionsState("ro", "fillfactor=70"))
			run(relationOptionsRows("ro"))
		})
	})
	t.Run("identity_after_wait", func(t *testing.T) {
		relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
			relationOptionsSeed(run)
			var oid, rowType int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid,reltype FROM pg_class WHERE oid='ro'::regclass").Scan(&oid, &rowType); err != nil {
				t.Fatal(err)
			}
			holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
			defer holder.ExecContext(context.Background(), "ROLLBACK")
			fixture.RowAffected(t, p.Ctx, holder, "ALTER TABLE ro SET(fillfactor=70)", prepared, 0)
			pid, blocker := 0, 0
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, waiter)
				blocker = fixture.LockPID(t, p.Ctx, holder)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			done := make(chan fixture.RelationOutcome, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					fixture.LockResult(t, done)
				}
			}()
			go relationOptionsWaitingAlter(ctx, waiter, "ALTER TABLE ro SET(fillfactor=80)", prepared, done)
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, pid, blocker, fixture.LockOID(t, p.Ctx, "ro"), "ShareUpdateExclusiveLock")
			} else {
				relationOptionsNativeWait(t, ctx, done)
			}
			if b.PG {
				fixture.LockExec(t, p.Ctx, holder, "ALTER TABLE ro RENAME TO ro_renamed", 0)
				fixture.LockExec(t, p.Ctx, holder, "COMMIT", 0)
			} else {
				// Native transactional RENAME is an existing capability boundary,
				// not an identity change exercised by this queued SQL witness.
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, relationOptionsError("ALTER TABLE ro RENAME TO ro_renamed", "0A000", "statement is unsupported in an explicit transaction", ""))
				fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
			}
			outcome := fixture.LockResult(t, done)
			joined = true
			target := "ro"
			if b.PG {
				want := relationOptionsDiagnostic("42P01", `relation "ro" does not exist`, "")
				fixture.AssertDiagnostic(t, outcome.Err, &want)
				if outcome.Result != nil {
					t.Fatal("stale-name ALTER produced result")
				}
				target = "ro_renamed"
				run(relationOptionsState(target, "fillfactor=70"))
			} else {
				if outcome.Err != nil || outcome.Result == nil {
					t.Fatal("queued ALTER after unsupported rename rollback", outcome.Err)
				}
				if count, err := outcome.Result.RowsAffected(); err != nil || count != 0 {
					t.Fatal(count, err)
				}
				run(relationOptionsState(target, "fillfactor=80"))
			}
			run(relationOptionsRows(target))
			run(fixture.Select("SELECT oid=$1::oid AS same_oid,reltype=$2::oid AS same_type FROM pg_class WHERE oid='"+target+"'::regclass", []string{"same_oid", "same_type"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, oid, rowType))
		})
	})
	t.Run("authority_after_wait", func(t *testing.T) {
		relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
			fixture.RoleReferenceCleanup(t, []string{"ro_lock_owner", "ro_lock_actor"})
			run(fixture.Exec("CREATE ROLE ro_lock_owner", 0))
			run(fixture.Exec("CREATE ROLE ro_lock_actor", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ro_lock_actor", 0))
			relationOptionsSeed(run)
			run(fixture.Exec("ALTER TABLE ro OWNER TO ro_lock_owner", 0))
			run(fixture.Exec("GRANT ro_lock_owner TO ro_lock_actor", 0))
			holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
			defer waiter.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
			defer holder.ExecContext(context.Background(), "ROLLBACK")
			fixture.RowAffected(t, p.Ctx, holder, "ALTER TABLE ro SET(fillfactor=70)", prepared, 0)
			fixture.LockExec(t, p.Ctx, waiter, "SET SESSION AUTHORIZATION ro_lock_actor", 0)
			query := "ALTER TABLE ro SET(fillfactor=80)"
			pid, blocker := 0, 0
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, waiter)
				blocker = fixture.LockPID(t, p.Ctx, holder)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			done := make(chan fixture.RelationOutcome, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					fixture.LockResult(t, done)
				}
			}()
			go relationOptionsWaitingAlter(ctx, waiter, query, prepared, done)
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, pid, blocker, fixture.LockOID(t, p.Ctx, "ro"), "ShareUpdateExclusiveLock")
			} else {
				relationOptionsNativeWait(t, ctx, done)
			}
			run(fixture.Exec("REVOKE ro_lock_owner FROM ro_lock_actor", 0))
			fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
			outcome := fixture.LockResult(t, done)
			joined = true
			want := relationOptionsDiagnostic("42501", "must be owner of table ro", "")
			fixture.AssertDiagnostic(t, outcome.Err, &want)
			if outcome.Result != nil {
				t.Fatal("authority failure published result")
			}
			run(relationOptionsState("ro", "fillfactor=70"))
			run(relationOptionsRows("ro"))
			fixture.LockExec(t, p.Ctx, waiter, "RESET SESSION AUTHORIZATION", 0)
			run(fixture.Exec("DROP TABLE ro", 0))
			run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM ro_lock_actor", 0))
			run(fixture.Exec("DROP ROLE ro_lock_actor,ro_lock_owner", 0))
		})
	})
}

func relationOptionsWaitingAlter(ctx context.Context, c *sql.Conn, query string, prepared bool, done chan<- fixture.RelationOutcome) {
	done <- fixture.RowExecute(ctx, c, query, prepared)
}

// Match the live operation and relation wait together, following the fixture's
// active-provider observer; no elapsed-time inference and no completion escape.
func relationOptionsNativeWait(t *testing.T, ctx context.Context, done chan fixture.RelationOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stacks := make([]byte, 64<<10)
	for {
		n := runtime.Stack(stacks, true)
		for n == len(stacks) {
			stacks = make([]byte, len(stacks)*2)
			n = runtime.Stack(stacks, true)
		}
		for _, frames := range bytes.Split(stacks[:n], []byte("\n\n")) {
			if at := bytes.Index(frames, []byte("\ncreated by ")); at >= 0 {
				frames = frames[:at]
			}
			if bytes.Contains(frames, []byte("relationOptionsWaitingAlter")) && bytes.Contains(frames, []byte("(*relationRequest).wait")) {
				return
			}
		}
		select {
		case outcome := <-done:
			done <- outcome
			t.Fatal("ALTER completed before lock admission", outcome.Err)
		case <-ctx.Done():
			t.Fatal("native lock admission", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}
