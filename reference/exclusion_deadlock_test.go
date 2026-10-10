package reference_test

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestReferenceExclusionConcurrencyDeferredDeadlock(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE TABLE t(v INT,owner TEXT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
		if b.pg {
			run(a, referenceExec("SET deadlock_timeout='2s'", 0))
			run(peer, referenceExec("SET deadlock_timeout='100ms'", 0))
		}
		run(a, referenceExec("BEGIN", 0))
		run(peer, referenceExec("BEGIN", 0))
		run(a, referenceExec("INSERT INTO t VALUES(1,'a')", 1))
		run(peer, referencePrepared(referenceExec("INSERT INTO t VALUES(2,'b')", 1)))
		// Crossed admissions encounter the other owner's initial key. Each
		// owner now has its own potential-conflict recheck obligation. All
		// four INSERTs must return before either COMMIT begins.
		run(a, referenceExec("INSERT INTO t VALUES(2,'a')", 1))
		run(peer, referencePrepared(referenceExec("INSERT INTO t VALUES(1,'b')", 1)))
		var pids [2]int
		var xids [2]string
		if b.pg {
			for i, c := range []referenceExecutor{a, peer} {
				rows, err := c.QueryContext(p.ctx, "SELECT pg_backend_pid(),pg_current_xact_id()::text")
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
		first := referenceRowAsync(t, p.ctx, a, "COMMIT", false, false)
		if b.pg {
			referenceRowWait(t, p.ctx, pids[0], pids[1])
		}
		second := referenceRowAsync(t, p.ctx, peer, "COMMIT", false, false)
		// Join both bounded workers before assigning a victim. Readiness ordering
		// is not an error outcome and cannot identify the deadlock victim.
		outcomes := [2]relationOutcome{referenceLockResult(t, first), referenceLockResult(t, second)}
		victim := -1
		for i, out := range outcomes {
			if out.err != nil {
				if victim != -1 {
					t.Fatal("both deferred commits failed", outcomes[0].err, outcomes[1].err)
				}
				victim = i
				if out.result != nil {
					t.Fatal("failed commit returned result")
				}
			} else {
				referenceLockAffected(t, out.result, 0)
			}
		}
		if victim == -1 {
			t.Fatal("conflicting deferred transactions both committed")
		}
		d := referenceExtractDiagnostic(t, outcomes[victim].err)
		want := controlsReferenceDiagnostic("40P01", "deadlock detected")
		want.Hint = "See server log for query details."
		if b.pg {
			other := 1 - victim
			want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", pids[victim], xids[other], pids[other], pids[other], xids[victim], pids[victim])
			pattern := regexp.MustCompile(`^while checking exclusion constraint on tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`)
			if !pattern.MatchString(d.Context) {
				t.Fatal("deadlock exclusion physical Context", d.Context)
			}
			want.Context = d.Context
			referenceAssertDiagnostic(t, outcomes[victim].err, &want)
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
		run(a, referenceSelect("SELECT v,owner FROM t ORDER BY v", []string{"v", "owner"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), owner}, {int64(2), owner}}))
		run(peer, referenceErrorDiagnostic("INSERT INTO t VALUES(1,'fresh')", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(a, referenceExec("BEGIN", 0))
		run(a, referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(a, referenceExec("ROLLBACK", 0))
	})
}
