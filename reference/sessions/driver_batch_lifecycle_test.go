package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDriverBatchTransactionBoundaries(t *testing.T) {
	cases := []struct {
		name, entry, q, code, message, notice, recovery string
		want                                            []int64
	}{
		{name: "implicit_success", q: "INSERT INTO batch_t VALUES(1); INSERT INTO batch_t VALUES(2)", want: []int64{1, 2, 7}},
		{name: "implicit_failure", q: "INSERT INTO batch_t VALUES(1); SELECT missing FROM batch_t; INSERT INTO batch_t VALUES(2)", code: "42703", message: `column "missing" does not exist`, want: []int64{7}},
		{name: "promotion", q: "INSERT INTO batch_t VALUES(1); BEGIN; INSERT INTO batch_t VALUES(2)", recovery: "ROLLBACK", want: []int64{7}},
		{name: "explicit_success", entry: "BEGIN", q: "INSERT INTO batch_t VALUES(1); INSERT INTO batch_t VALUES(2)", recovery: "ROLLBACK", want: []int64{7}},
		{name: "skipped_recovery", q: "BEGIN; INSERT INTO batch_t VALUES(1); SELECT missing FROM batch_t; ROLLBACK", code: "42703", message: `column "missing" does not exist`, recovery: "ROLLBACK", want: []int64{7}},
		{name: "commit_suffix_failure", q: "BEGIN; INSERT INTO batch_t VALUES(1); COMMIT; INSERT INTO batch_t VALUES(2); SELECT missing FROM batch_t", code: "42703", message: `column "missing" does not exist`, want: []int64{1, 7}},
		{name: "existing_commit", entry: "BEGIN", q: "INSERT INTO batch_t VALUES(1); COMMIT; INSERT INTO batch_t VALUES(2); SELECT missing FROM batch_t", code: "42703", message: `column "missing" does not exist`, want: []int64{1, 7}},
		{name: "existing_rollback", entry: "BEGIN", q: "INSERT INTO batch_t VALUES(1); ROLLBACK; INSERT INTO batch_t VALUES(2)", want: []int64{2, 7}},
		{name: "implicit_commit", q: "INSERT INTO batch_t VALUES(1); COMMIT; INSERT INTO batch_t VALUES(2)", notice: "25P01", want: []int64{1, 2, 7}},
		{name: "implicit_rollback", q: "INSERT INTO batch_t VALUES(1); ROLLBACK; INSERT INTO batch_t VALUES(2)", notice: "25P01", want: []int64{2, 7}},
		{name: "savepoint", q: "INSERT INTO batch_t VALUES(1); SAVEPOINT s", code: "25P01", message: "SAVEPOINT can only be used in transaction blocks", want: []int64{7}},
		{name: "commit_chain", q: "INSERT INTO batch_t VALUES(1); COMMIT AND CHAIN", code: "25P01", message: "COMMIT AND CHAIN can only be used in transaction blocks", want: []int64{7}},
		{name: "rollback_chain", q: "INSERT INTO batch_t VALUES(1); ROLLBACK AND CHAIN", code: "25P01", message: "ROLLBACK AND CHAIN can only be used in transaction blocks", want: []int64{7}},
		{name: "repeated_begin", entry: "BEGIN", q: "BEGIN; INSERT INTO batch_t VALUES(1)", notice: "25001", recovery: "ROLLBACK", want: []int64{7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused, peer *sql.Conn) {
				f := fixture.NewControlsReferenceFixture(t, p, b.PG)
				a := f.A
				batchSeed(t, p, b, a)
				f.NoticesWant(t, "", "")
				if tc.entry != "" {
					batchExec(t, p, b, a, tc.entry, 0)
				}
				if tc.code != "" {
					w := batchDiag(tc.code, tc.message)
					if tc.code == "42703" {
						w.Position = batchPosition(tc.q, "missing")
					}
					batchError(t, p, a, tc.q, w)
				} else {
					r, err := a.ExecContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					if r == nil {
						t.Fatal("missing result")
					}
				}
				msg := "there is no transaction in progress"
				if tc.notice == "25001" {
					msg = "there is already a transaction in progress"
				}
				if tc.notice == "" {
					msg = ""
				}
				f.NoticesWant(t, tc.notice, msg)
				if tc.recovery != "" {
					batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", 7)
					if tc.code != "" {
						batchError(t, p, a, "SELECT i FROM batch_t", batchDiag("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
					}
					batchExec(t, p, b, a, tc.recovery, 0)
				}
				batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", tc.want...)
			})
		})
	}
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		tx, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		batchExec(t, p, b, tx, "INSERT INTO batch_t VALUES(1); INSERT INTO batch_t VALUES(2)", 1)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		batchExec(t, p, b, a, "BEGIN; SAVEPOINT s", 0)
		q := "INSERT INTO batch_t VALUES(1); SELECT missing FROM batch_t"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		batchError(t, p, a, q, w)
		batchExec(t, p, b, a, "ROLLBACK TO s; INSERT INTO batch_t VALUES(2); COMMIT", 0)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t ORDER BY i", 2, 7)
	})
	batchParseControls(t)
}
func TestReferenceDriverBatchClockSettingsTimeouts(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		r := batchQuery(t, p, a, "SELECT statement_timestamp(),transaction_timestamp(); BEGIN; SELECT statement_timestamp(),transaction_timestamp(); COMMIT; SELECT statement_timestamp(),transaction_timestamp()")
		var firstS, firstT time.Time
		for i := range 3 {
			if i > 0 && !r.NextResultSet() {
				t.Fatal(r.Err())
			}
			if !r.Next() {
				t.Fatal(r.Err())
			}
			var s, tx time.Time
			if err := r.Scan(&s, &tx); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				firstS, firstT = s, tx
			} else {
				if !s.Equal(firstS) {
					t.Fatal("message clock changed")
				}
				if i == 1 && !tx.Equal(firstT) {
					t.Fatal("promotion replaced transaction clock")
				}
				if i == 2 && tx.Before(firstT) {
					t.Fatal("new segment clock regressed")
				}
			}
			if r.Next() {
				t.Fatal("extra clock row")
			}
			if err := r.Err(); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
		batchSets(t, batchQuery(t, p, a, "SET application_name='session'; SET LOCAL application_name='local'; SHOW application_name"), batchSet{[]string{"application_name"}, []string{"TEXT"}, [][]any{{"local"}}, false})
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"session"}}))
		q := "SET application_name='discard'; SELECT missing"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		batchError(t, p, a, q, w)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"session"}}))
		batchSets(t, batchQuery(t, p, a, "SET standard_conforming_strings=off; SELECT 'a\\b' AS s"), batchSet{[]string{"s"}, []string{"TEXT"}, [][]any{{"a\\b"}}, false})
		batchExec(t, p, b, a, "SET standard_conforming_strings=on", 0)
	})
	for _, setting := range []string{"statement_timeout", "lock_timeout"} {
		t.Run(setting, func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				f := newTimeoutReferenceFixture(t, p, b)
				release := f.holdBlocked()
				defer release()
				q := "SET " + setting + "='300ms'; INSERT INTO timeout_sentinel VALUES(8); SELECT v FROM timeout_blocked"
				o := f.blocked("query", q, nil)
				code := "57014"
				if setting == "lock_timeout" {
					code = "55P03"
				}
				want := timeoutReferenceWant(code)
				if setting == "lock_timeout" {
					want.Position = batchPosition(q, "timeout_blocked")
				}
				fixture.AssertDiagnostic(t, o.Err, &want)
				f.rows(f.owner, 7)
			})
		})
	}
}
func TestReferenceDriverBatchCancellationLifecycle(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				f := newTimeoutReferenceFixture(t, p, b)
				release := f.holdBlocked()
				defer release()
				if explicit {
					f.exec(f.owner, "BEGIN", 0)
				}
				ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					_, err := f.owner.ExecContext(ctx, "INSERT INTO timeout_sentinel VALUES(8); SELECT v FROM timeout_blocked; INSERT INTO timeout_sentinel VALUES(9)")
					done <- err
				}()
				t.Cleanup(func() {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("cancellation worker did not join")
					}
				})
				if b.PG {
					fixture.LockWaitBehind(t, p.Ctx, f.ownerPID, f.peerPID, f.blockedOID, "AccessShareLock")
					cancel()
				}
				err := <-done
				done <- nil
				if b.PG {
					w := batchDiag("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, err, &w)
				} else if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				if b.PG {
					// pq sends the cancellation diagnostic, then retires this physical owner.
					// Its ordinary sentinel is not a second SQL diagnostic or a recoverable block.
					_, reuseErr := f.owner.ExecContext(p.Ctx, "SELECT v FROM timeout_sentinel")
					timeoutReferenceDeadError(t, reuseErr)
					f.rows(f.peer, 7)
				} else {
					if explicit {
						batchError(t, p, f.owner, "SELECT v FROM timeout_sentinel", batchDiag("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						f.exec(f.owner, "ROLLBACK", 0)
					}
					f.rows(f.owner, 7)
				}
			})
		})
	}
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		r, err := a.QueryContext(ctx, "INSERT INTO batch_t VALUES(8); SELECT i FROM batch_t ORDER BY i; SELECT i FROM batch_t")
		if err != nil {
			t.Fatal(err)
		}
		if !r.Next() {
			t.Fatal(r.Err())
		}
		var saved int64
		if err := r.Scan(&saved); err != nil {
			t.Fatal(err)
		}
		// pq is streaming: drain ReadyForQuery before claiming late cancellation.
		// Native has already eagerly completed the message when Query returns.
		if b.PG {
			batchReadSet(t, r, batchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}, false})
			if !r.NextResultSet() {
				t.Fatal(r.Err())
			}
			batchReadSet(t, r, batchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}, true})
			if r.NextResultSet() || r.Err() != nil {
				t.Fatal(r.Err())
			}
		}
		cancel()
		r.Close()
		if saved != 7 {
			t.Fatal(saved)
		}
		batchValues(t, p, b, peer, "SELECT i FROM batch_t ORDER BY i", 7, 8)
	})
	batchRetainedControls(t)
}
