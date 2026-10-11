package reference_test

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExclusionConcurrencyDeferredDeadlock(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE t(v INT,owner TEXT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
		if b.PG {
			run(a, fixture.Exec("SET deadlock_timeout='2s'", 0))
			run(peer, fixture.Exec("SET deadlock_timeout='100ms'", 0))
		}
		run(a, fixture.Exec("BEGIN", 0))
		run(peer, fixture.Exec("BEGIN", 0))
		run(a, fixture.Exec("INSERT INTO t VALUES(1,'a')", 1))
		run(peer, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,'b')", 1)))
		// Crossed admissions encounter the other owner's initial key. Each
		// owner now has its own potential-conflict recheck obligation. All
		// four INSERTs must return before either COMMIT begins.
		run(a, fixture.Exec("INSERT INTO t VALUES(2,'a')", 1))
		run(peer, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(1,'b')", 1)))
		var pids [2]int
		var xids [2]string
		if b.PG {
			for i, c := range []fixture.Executor{a, peer} {
				rows, err := c.QueryContext(p.Ctx, "SELECT pg_backend_pid(),pg_current_xact_id()::text")
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing transaction identity")
				}
				if err := rows.Scan(&pids[i], &xids[i]); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("extra identity rows", rows.Err())
				}
				rows.Close()
			}
		}
		first := fixture.RowAsync(t, p.Ctx, a, "COMMIT", false, false)
		if b.PG {
			fixture.RowWait(t, p.Ctx, pids[0], pids[1])
		}
		second := fixture.RowAsync(t, p.Ctx, peer, "COMMIT", false, false)
		// Join both bounded workers before assigning a victim. Readiness ordering
		// is not an error outcome and cannot identify the deadlock victim.
		outcomes := [2]fixture.RelationOutcome{fixture.LockResult(t, first), fixture.LockResult(t, second)}
		victim := -1
		for i, out := range outcomes {
			if out.Err != nil {
				if victim != -1 {
					t.Fatal("both deferred commits failed", outcomes[0].Err, outcomes[1].Err)
				}
				victim = i
				if out.Result != nil {
					t.Fatal("failed commit returned result")
				}
			} else {
				fixture.LockAffected(t, out.Result, 0)
			}
		}
		if victim == -1 {
			t.Fatal("conflicting deferred transactions both committed")
		}
		d := fixture.ExtractDiagnostic(t, outcomes[victim].Err)
		want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
		want.Hint = "See server log for query details."
		if b.PG {
			other := 1 - victim
			want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", pids[victim], xids[other], pids[other], pids[other], xids[victim], pids[victim])
			pattern := regexp.MustCompile(`^while checking exclusion constraint on tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`)
			if !pattern.MatchString(d.Context) {
				t.Fatal("deadlock exclusion physical Context", d.Context)
			}
			want.Context = d.Context
			fixture.AssertDiagnostic(t, outcomes[victim].Err, &want)
		} else {
			lines := strings.Split(d.Detail, "\n")
			if len(lines) != 2 {
				t.Fatal("expected two completion edges", d.Detail)
			}
			pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for transaction completion; blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
			x, y := pattern.FindStringSubmatch(lines[0]), pattern.FindStringSubmatch(lines[1])
			if x == nil || y == nil || x[1] == y[1] || x[2] == y[2] || x[1] != y[4] || x[2] != y[5] || x[3] != y[6] || y[1] != x[4] || y[2] != x[5] || y[3] != x[6] {
				t.Fatal("completion cycle identities do not close", d.Detail)
			}
			d.Detail = "" // Only opaque native identities differ; edge grammar checked above.
			if !reflect.DeepEqual(d, want) {
				t.Fatal(d, want)
			}
		}
		owner := "a"
		if victim == 0 {
			owner = "b"
		}
		run(a, fixture.Select("SELECT v,owner FROM t ORDER BY v", []string{"v", "owner"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), owner}, {int64(2), owner}}))
		run(peer, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'fresh')", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(a, fixture.Exec("BEGIN", 0))
		run(a, fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(a, fixture.Exec("ROLLBACK", 0))
	})
}
