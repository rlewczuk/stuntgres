package reference_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInheritedCancellation(t *testing.T) {
	partitionInheritedScenario(t, func(s *partitionInheritedCase) {
		t := s.t
		s.tree(",CONSTRAINT pi_guard CHECK(k>=0)", "")
		s.exec("INSERT INTO pi_p VALUES(1,7,1),(11,8,NULL)", 2)
		before := make(map[string]string)
		for _, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
			before[table] = s.oid(table, "pi_guard")
		}
		holder, _ := fixture.LockConnections(t, s.p.Ctx, s.b.DB)
		q := "ALTER TABLE pi_p DROP CONSTRAINT pi_guard,ADD CONSTRAINT pi_ck CHECK(b=1),ALTER COLUMN a SET NOT NULL"
		x := fixture.Executor(s.conn)
		if s.prepared {
			stmt, err := s.conn.PrepareContext(s.p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x = fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: q}
		}
		pid, blocker := 0, 0
		var oid int64
		if s.b.PG {
			pid, blocker = fixture.LockPID(t, s.p.Ctx, s.conn), fixture.LockPID(t, s.p.Ctx, holder)
			oid = fixture.LockOID(t, s.p.Ctx, "pi_hi")
		}
		tx, err := holder.BeginTx(s.p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// A consumed physical-only read retains ACCESS SHARE until rollback,
		// without requiring unsupported hierarchy LOCK TABLE admission.
		fixture.RunStep(t, s.p.Ctx, tx, s.recorder, fixture.Select(
			"SELECT k,a,b FROM ONLY pi_hi", []string{"k", "a", "b"},
			[]string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(8), nil}},
		))
		ctx, cancel := context.WithCancel(s.p.Ctx)
		done := make(chan fixture.RelationOutcome, 1)
		go func() {
			result, err := x.ExecContext(ctx, q)
			done <- fixture.RelationOutcome{Result: result, Err: err}
		}()
		joined := false
		defer func() {
			cancel()
			if !joined {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("descendant ALTER worker did not join")
				}
			}
		}()
		if s.b.PG {
			fixture.LockWaitBehind(t, s.p.Ctx, pid, blocker, oid, "AccessExclusiveLock")
			var ack bool
			if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ack); err != nil || !ack {
				t.Fatal(ack, err)
			}
		} else {
			// Observe the actual worker parked in descendant admission. The
			// physical-only reader above is the sole conflicting holder.
			stacks := make([]byte, 64<<10)
			for {
				select {
				case outcome := <-done:
					joined = true
					t.Fatal("ALTER completed before descendant admission", outcome.Err)
				case <-s.p.Ctx.Done():
					t.Fatal(s.p.Ctx.Err())
				default:
				}
				n := runtime.Stack(stacks, true)
				for n == len(stacks) {
					stacks = make([]byte, len(stacks)*2)
					n = runtime.Stack(stacks, true)
				}
				waiting := false
				for _, frames := range bytes.Split(stacks[:n], []byte("\n\n")) {
					if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
						frames = frames[:created]
					}
					if bytes.Contains(frames, []byte("(*relationRequest).wait(")) &&
						bytes.Contains(frames, []byte("(*Engine).admitInheritanceAlter(")) &&
						bytes.Contains(frames, []byte("TestReferencePartitionInheritedCancellation")) {
						waiting = true
						break
					}
				}
				if waiting {
					break
				}
				runtime.Gosched()
			}
			cancel()
		}
		outcome := fixture.LockResult(t, done)
		joined = true
		if s.b.PG {
			want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, outcome.Err, &want)
		} else if !errors.Is(outcome.Err, context.Canceled) {
			t.Fatal(outcome.Err)
		}
		if outcome.Result != nil {
			t.Fatal("canceled ALTER returned a result")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		for i, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
			s.facts(table, [][]any{partitionInheritedFact("pi_guard", "c", i == 0, int64(min(i, 1)), "{1}")})
			s.sameOID(table, "pi_guard", before[table])
			s.nullability(table, false)
		}
		s.values("SELECT k,a,b FROM pi_p ORDER BY k", [][]any{{int64(1), int64(7), int64(1)}, {int64(11), int64(8), nil}}, []bool{true, true, true})
		// Reuse the canceled owner (and held statement), proving no stale holds.
		fixture.RunStep(t, s.p.Ctx, x, s.recorder, fixture.Exec(q, 0))
		for i, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
			s.facts(table, [][]any{partitionInheritedFact("pi_ck", "c", i == 0, int64(min(i, 1)), "{3}"), partitionInheritedFact("pi_p_a_not_null", "n", i == 0, int64(min(i, 1)), "{2}")})
			s.nullability(table, true)
		}
		s.fail("INSERT INTO pi_p VALUES(12,7,2)", partitionInheritedCheckFailure("pi_hi", "pi_ck", "12, 7, 2"))
		s.fail("INSERT INTO pi_lo VALUES(2,NULL,1)", partitionInheritedNullFailure("pi_lo", "2, null, 1"))
	})
}
