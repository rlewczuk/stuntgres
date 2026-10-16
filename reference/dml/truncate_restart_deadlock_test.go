package reference_test

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Variable identities are observations, not expected allocator numbers. The
// English fields and both mode/resource/blocker edges are independent contracts.
func truncateRestartDeadlockDiagnostic(t *testing.T, err error, pg bool, pids [2]int, table, sequence, database int64) {
	t.Helper()
	d := fixture.ExtractDiagnostic(t, err)
	want := truncateWant("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	want.Detail = d.Detail
	fixture.AssertDiagnostic(t, err, &want)
	fixture.State(t, err, "40P01")
	if !pg {
		fixture.DeadlockPublicError(t, err, "")
	}
	lines := strings.Split(strings.TrimSpace(d.Detail), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly two ownership cycle edges: %q", d.Detail)
	}
	physical := regexp.MustCompile(`^Process ([1-9][0-9]*) waits for (AccessShareLock|AccessExclusiveLock) on relation ([1-9][0-9]*) of database ([1-9][0-9]*); blocked by process ([1-9][0-9]*)\.$`)
	logical := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for (AccessShareLock|AccessExclusiveLock) on relation ([1-9][0-9]*) \(class 1259, sub-ID 0, epoch ([1-9][0-9]*)\); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	seen := map[int]bool{}
	for _, line := range lines {
		var from, to int
		var mode string
		var relation int64
		if pg {
			m := physical.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("complete PG edge: %q", line)
			}
			from, _ = strconv.Atoi(m[1])
			to, _ = strconv.Atoi(m[5])
			mode = m[2]
			relation, _ = strconv.ParseInt(m[3], 10, 64)
			db, _ := strconv.ParseInt(m[4], 10, 64)
			if db != database {
				t.Fatalf("wrong cycle database: %q", line)
			}
		} else {
			m := logical.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("complete native edge: %q", line)
			}
			from, _ = strconv.Atoi(m[2])
			to, _ = strconv.Atoi(m[8])
			mode = m[4]
			relation, _ = strconv.ParseInt(m[5], 10, 64)
		}
		if seen[from] {
			t.Fatalf("duplicate cycle participant: %q", d.Detail)
		}
		seen[from] = true
		if from == pids[0] {
			if to != pids[1] || mode != "AccessExclusiveLock" || relation != sequence {
				t.Fatalf("reset sequence edge: %q", line)
			}
		} else if from == pids[1] {
			if to != pids[0] || mode != "AccessShareLock" || relation != table {
				t.Fatalf("OWNED BY table edge: %q", line)
			}
		} else {
			t.Fatalf("unexpected participant: %q", line)
		}
	}
	if !seen[pids[0]] || !seen[pids[1]] {
		t.Fatalf("unclosed observed participant cycle: %q", d.Detail)
	}
}

func TestReferenceTruncateRestartOwnershipDeadlock(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		for _, q := range []string{
			"CREATE SEQUENCE s START 10",
			"CREATE TABLE p(id INT DEFAULT nextval('s') PRIMARY KEY,other INT)",
			"ALTER SEQUENCE s OWNED BY p.id",
			"CREATE TABLE q(id SERIAL)",
		} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "INSERT INTO p DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO p DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO q DEFAULT VALUES", 1)
		pids := [2]int{truncateRestartPID(t, p, b, f.A), truncateRestartPID(t, p, b, f.B)}
		var table, sequence, database int64
		if err := f.A.QueryRowContext(p.Ctx, "SELECT 'p'::regclass::oid").Scan(&table); err != nil {
			t.Fatal(err)
		}
		if err := f.A.QueryRowContext(p.Ctx, "SELECT 's'::regclass::oid").Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			if err := f.A.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
		}
		if table <= 0 || sequence <= 0 || table == sequence {
			t.Fatal("invalid observed resource identities")
		}
		resetQ, alterQ := "TRUNCATE p,q RESTART IDENTITY", "ALTER SEQUENCE s OWNED BY p.other"
		reset := truncateRestartRoute(t, p, f.A, resetQ, held)
		alter := truncateRestartRoute(t, p, f.B, alterQ, held)
		defer f.A.ExecContext(p.Ctx, "ROLLBACK")
		defer f.B.ExecContext(p.Ctx, "ROLLBACK")
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateExec(t, f, f.B, "BEGIN", 0)
		// Public SQL retains sequence SRX without table AS. The association
		// removal is private to B and must disappear if B becomes the victim.
		truncateExec(t, f, f.B, "ALTER SEQUENCE s OWNED BY NONE", 0)
		ctx, cancel := context.WithTimeout(p.Ctx, 8*time.Second)
		finished := []chan struct{}{make(chan struct{})}
		defer func() {
			cancel()
			for _, done := range finished {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("ownership cycle worker did not join")
				}
			}
		}()
		resetDone := finished[0]
		joinReset := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(resetDone)
			r, err := reset.ExecContext(ctx, resetQ)
			return fixture.RelationOutcome{Result: r, Err: err}
		})
		truncateRestartWait(t, p, b, pids[1], pids[0], "s", "ShareRowExclusiveLock", "AccessExclusiveLock")
		var prefix int64
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND relation IN ('p'::regclass::oid,'q'::regclass::oid) AND mode='AccessExclusiveLock' AND granted`, pids[0]).Scan(&prefix); err != nil || prefix != 2 {
			t.Fatalf("actual table-AX prefix %d: %v", prefix, err)
		}
		alterDone := make(chan struct{})
		finished = append(finished, alterDone)
		joinAlter := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(alterDone)
			r, err := alter.ExecContext(ctx, alterQ)
			return fixture.RelationOutcome{Result: r, Err: err}
		})
		// Both operations are already running under the same bounded context;
		// joining in this order does not select the victim by arrival order.
		outcomes := [2]fixture.RelationOutcome{joinReset(), joinAlter()}
		if (outcomes[0].Err == nil) == (outcomes[1].Err == nil) {
			t.Fatalf("expected one victim and one survivor: %#v", outcomes)
		}
		victim := 0
		if outcomes[1].Err != nil {
			victim = 1
		}
		if outcomes[victim].Result != nil {
			t.Fatal("deadlock victim returned a result")
		}
		truncateRestartDeadlockDiagnostic(t, outcomes[victim].Err, b.PG, pids, table, sequence, database)
		fixture.LockAffected(t, outcomes[1-victim].Result, 0)
		if _, err := outcomes[1-victim].Result.LastInsertId(); err == nil {
			t.Fatal("utility exposed LastInsertId")
		}
		victimConn := f.A
		if victim == 1 {
			victimConn = f.B
		}
		truncateRestartError(t, f, victimConn, "SELECT true", truncateWant("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		truncateExec(t, f, victimConn, "ROLLBACK", 0)
		column := int64(1)
		if victim == 1 {
			truncateRows(t, f, f.A, "p")
			truncateRows(t, f, f.A, "q")
			truncateRestartState(t, f, f.A, "s", 10, false)
			truncateRestartState(t, f, f.A, "q_id_seq", 1, false)
			truncateExec(t, f, f.A, "COMMIT", 0)
		} else {
			column = 2
			truncateRows(t, f, f.B, "p", 10, 11)
			truncateRows(t, f, f.B, "q", 1)
			truncateRestartState(t, f, f.B, "s", 11, true)
			truncateRestartState(t, f, f.B, "q_id_seq", 1, true)
			truncateExec(t, f, f.B, "COMMIT", 0)
		}
		// Exact committed ownership distinguishes reverted NONE from a real
		// successful reassociation; no allocator equality is assumed across modes.
		truncateRun(t, f, f.A, fixture.Select("SELECT refobjsubid FROM pg_depend WHERE classid=1259 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND deptype='a'", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{column}}, sequence, table))
		if victim == 1 {
			truncateRows(t, f, f.B, "p")
			truncateRows(t, f, f.B, "q")
			truncateRestartState(t, f, f.B, "s", 10, false)
		} else {
			truncateRows(t, f, f.A, "p", 10, 11)
			truncateRows(t, f, f.A, "q", 1)
			truncateRestartState(t, f, f.A, "s", 11, true)
		}
		// Recover both physical sessions and their original held handles.
		truncateExec(t, f, alter, alterQ, 0)
		truncateExec(t, f, reset, resetQ, 0)
		truncateRestartState(t, f, f.A, "s", 10, false)
		truncateRestartState(t, f, f.A, "q_id_seq", 1, false)
		truncateExec(t, f, f.A, "INSERT INTO p DEFAULT VALUES", 1)
		truncateExec(t, f, f.B, "INSERT INTO q DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "p", 10)
		truncateRows(t, f, f.B, "q", 1)
	})
}
