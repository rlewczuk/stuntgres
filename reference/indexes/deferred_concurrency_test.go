package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredUniqueDeadlock(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
		peer := fixture.ConcurrentIndexConn(t, p, b)
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,owner TEXT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
		run(fixture.Exec("BEGIN", 0))
		step(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'a')", 1))
		step(fixture.Exec("INSERT INTO t VALUES(2,'b')", 1))
		run(fixture.Exec("INSERT INTO t VALUES(2,'a')", 1))
		step(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(1,'b')", 1)))
		var pids [2]int
		var xids [2]string
		if b.PG {
			for i, c := range []*sql.Conn{a, peer} {
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),pg_current_xact_id()::text").Scan(&pids[i], &xids[i]); err != nil {
					t.Fatal(err)
				}
			}
		}
		first := fixture.DefinitionLockOperation(t, p.Ctx, p, b, a, peer, "COMMIT", "transactionid", "ShareLock", 0, 0)
		second := fixture.RowAsync(t, p.Ctx, peer, "COMMIT", false, false)
		out := [2]fixture.RelationOutcome{first(), fixture.LockResult(t, second)}
		victim := -1
		for i, result := range out {
			if result.Err != nil {
				if victim != -1 {
					t.Fatal("both commits failed", out[0].Err, out[1].Err)
				}
				victim = i
				if result.Result != nil {
					t.Fatal("failed commit returned Result")
				}
			} else {
				fixture.LockAffected(t, result.Result, 0)
			}
		}
		if victim < 0 {
			t.Fatal("both crossed-key owners committed")
		}
		got := fixture.ExtractDiagnostic(t, out[victim].Err)
		want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
		want.Hint = "See server log for query details."
		if b.PG {
			other := 1 - victim
			want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", pids[victim], xids[other], pids[other], pids[other], xids[victim], pids[victim])
			if !regexp.MustCompile(`^while inserting index tuple \([0-9]+,[1-9][0-9]*\) in relation "u"$`).MatchString(got.Context) {
				t.Fatal("deadlock tuple Context", got.Context)
			}
			want.Context = got.Context
		} else {
			lines := strings.Split(got.Detail, "\n")
			pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for transaction completion; blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
			if len(lines) != 2 {
				t.Fatal(got.Detail)
			}
			x, y := pattern.FindStringSubmatch(lines[0]), pattern.FindStringSubmatch(lines[1])
			if x == nil || y == nil || x[1] == y[1] || x[2] == y[2] || x[1] != y[4] || x[2] != y[5] || x[3] != y[6] || y[1] != x[4] || y[2] != x[5] || y[3] != x[6] {
				t.Fatal("invalid completion cycle", got.Detail)
			}
			got.Detail = ""
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
		owner := "a"
		if victim == 0 {
			owner = "b"
		}
		run(fixture.Select("SELECT v,owner FROM t ORDER BY v", []string{"v", "owner"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), owner}, {int64(2), owner}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("INSERT INTO t VALUES(9,'fresh')", 1))
	})
}

func TestReferenceDeferredUniqueSnapshot(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
		peer := fixture.ConcurrentIndexConn(t, p, b)
		run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("COMMIT", fixture.DeferredUniqueConflict("t", "u", "v", "1")))
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
