package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBlockingPidsStoredControls(t *testing.T) {
	blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		holder, hid := blockingConn(t, p, b)
		waiter, wid := blockingConn(t, p, b)
		observer, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, holder, "CREATE TABLE blocking_stored_lock(v INT)", "CREATE TABLE blocking_targets(pid INT)")
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec("INSERT INTO blocking_targets VALUES($1)", 1, wid))
		namespaceExec(t, p, b, observer, "CREATE VIEW blocking_stored AS SELECT pg_blocking_pids(pid) AS ordinary,pg_safe_snapshot_blocking_pids(pid) AS safe FROM blocking_targets")
		q := "SELECT ordinary FROM blocking_stored"
		held, err := observer.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		x := fixture.HeldStatement{Executor: observer, Statement: held, SQL: q}
		namespaceExec(t, p, b, holder, "BEGIN", "LOCK TABLE blocking_stored_lock IN ACCESS EXCLUSIVE MODE")
		namespaceExec(t, p, b, waiter, "BEGIN")
		await := blockingLock(t, p, waiter, "LOCK TABLE blocking_stored_lock IN ACCESS EXCLUSIVE MODE", false)
		blockingWait(t, p, b, observer, "pg_blocking_pids", wid, hid)
		// Retain the decoded owned result across release and native replay.
		check := func(executor fixture.Executor, ordinary string) {
			s := fixture.Select("SELECT ordinary,safe FROM blocking_stored", []string{"ordinary", "safe"}, []string{"_INT4", "_INT4"}, [][]any{{ordinary, "{}"}})
			s.CheckMetadata = blockingMetadata(b)
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Prepared(s))
		}
		// One actual live owner makes its canonical singleton independent of order.
		want := "{" + strconv.Itoa(hid) + "}"
		check(observer, want)
		rows, err := x.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		// The stored query has no parameters; held consumers execute that recipe.
		captured := blockingBag(t, b, rows)
		if len(captured) != 1 || captured[0] != hid {
			t.Fatal(captured, hid)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			target := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT ordinary,safe FROM blocking_stored", []string{"ordinary", "safe"}, []string{"_INT4", "_INT4"}, [][]any{{"{}", "{}"}})
				s.Prepared = prepared
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
			}
		}
		namespaceExec(t, p, b, holder, "COMMIT")
		blockingLockDone(t, await)
		check(observer, "{}")
		rows, err = x.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if got := blockingBag(t, b, rows); len(got) != 0 {
			t.Fatal("held recipe cached blocker", got)
		}
		if captured[0] != hid {
			t.Fatal("captured result mutated")
		}
		namespaceExec(t, p, b, waiter, "ROLLBACK")
	})
}
