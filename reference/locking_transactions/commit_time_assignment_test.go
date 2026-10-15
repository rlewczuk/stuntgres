package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCommitTimeAssignmentBoundaries(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, owner, "CREATE TABLE commit_time_assignment(v INT)", 0)
		baseline := commitTimeLast(t, p, observer)
		for _, explicit := range []bool{false, true} {
			for _, query := range []string{
				"DELETE FROM commit_time_assignment",
				"UPDATE commit_time_assignment SET v=v+1",
				"SELECT v FROM commit_time_assignment FOR UPDATE",
			} {
				if !t.Run(query+map[bool]string{false: "/autocommit", true: "/explicit"}[explicit], func(t *testing.T) {
					committed := !explicit
					defer func() {
						if !committed {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()
							if _, err := owner.ExecContext(ctx, "ROLLBACK"); err != nil {
								t.Error("explicit assignment cleanup", err)
							}
						}
					}()
					if explicit {
						fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
					}
					step := fixture.Exec(query, 0)
					if query == "SELECT v FROM commit_time_assignment FOR UPDATE" {
						step = fixture.Select(query, []string{"v"}, []string{"INT4"}, nil)
					}
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, step)
					commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
					if explicit {
						fixture.LockExec(t, p.Ctx, owner, "COMMIT", 0)
						committed = true
						commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
					}
				}) {
					return
				}
			}
		}
		// Equivalent operations on real rows must produce actual completions,
		// including a row lock even though it changes no user column values.
		for _, explicit := range []bool{false, true} {
			for _, step := range []fixture.Step{
				fixture.Exec("INSERT INTO commit_time_assignment VALUES(1)", 1),
				fixture.Exec("UPDATE commit_time_assignment SET v=2 WHERE v=1", 1),
				fixture.Select("SELECT v FROM commit_time_assignment FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}),
				fixture.Exec("DELETE FROM commit_time_assignment WHERE v=2", 1),
			} {
				if !t.Run(step.Query+map[bool]string{false: "/autocommit", true: "/explicit"}[explicit], func(t *testing.T) {
					committed := !explicit
					defer func() {
						if !committed {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()
							if _, err := owner.ExecContext(ctx, "ROLLBACK"); err != nil {
								t.Error("explicit assignment cleanup", err)
							}
						}
					}()
					before := commitTimeClock(t, p, observer)
					if explicit {
						fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
					}
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, step)
					if explicit {
						commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
						before = commitTimeClock(t, p, observer)
						fixture.LockExec(t, p.Ctx, owner, "COMMIT", 0)
						committed = true
					}
					after := commitTimeClock(t, p, observer)
					next := commitTimeLast(t, p, observer)
					if next.xid == baseline.xid {
						t.Fatal("actual mutation/row lock did not record a new completion")
					}
					commitTimeBounded(t, next, before, after)
					commitTimeLookup(t, p, owner, next)
					baseline = next
				}) {
					return
				}
			}
		}
		// Rolling back all staged rows must not unassign the real top-level XID.
		// Plain COMMIT is essential: PREPARE would itself assign an empty owner.
		fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, owner, "SAVEPOINT commit_time_written", 0)
		fixture.LockExec(t, p.Ctx, owner, "INSERT INTO commit_time_assignment VALUES(3)", 1)
		fixture.LockExec(t, p.Ctx, owner, "ROLLBACK TO commit_time_written", 0)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select("SELECT v FROM commit_time_assignment", []string{"v"}, []string{"INT4"}, nil))
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		before := commitTimeClock(t, p, observer)
		fixture.LockExec(t, p.Ctx, owner, "COMMIT", 0)
		after := commitTimeClock(t, p, observer)
		next := commitTimeLast(t, p, observer)
		if next.xid == baseline.xid {
			t.Fatal("savepoint rollback erased genuine assignment")
		}
		commitTimeBounded(t, next, before, after)
		commitTimeLookup(t, p, owner, next)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_assignment", []string{"v"}, []string{"INT4"}, nil))
		commitTimeSame(t, commitTimeLast(t, p, observer), next)
	})
}
