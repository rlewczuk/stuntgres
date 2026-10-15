package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeoutPoolNoReplay(t *testing.T) {
	for _, kind := range []string{"returned-idle", "accepted-insert", "healthy-statement"} {
		t.Run(kind, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				f := newTimeoutReferenceFixture(t, p, b)
				// The peer occupies one slot, leaving precisely the owner slot
				// for borrower return/replacement. No alternate healthy connection
				// may accidentally make this a pool rather than physical test.
				b.DB.SetMaxOpenConns(2)
				b.DB.SetMaxIdleConns(1)
				identity := settingsPhysical(t, f.owner)
				switch kind {
				case "returned-idle":
					f.set("idle_session_timeout", "250ms")
					if err := f.owner.Close(); err != nil {
						t.Fatal(err)
					}
					timeoutReferenceIdle(t, p.Ctx, 350*time.Millisecond)
					// Real pinned oracle: pq's FATAL sentinel permits safe
					// database/sql retry before new work. The new INSERT must
					// publish exactly once, independently of the expired owner.
					f.exec(b.DB, "INSERT INTO timeout_sentinel VALUES(99)", 1)
					if b.PG {
						f.serverFatal("57P05")
					}
					f.rows(f.peer, 7, 99)
					f.exec(f.peer, "DELETE FROM timeout_sentinel WHERE v=99", 1)
				case "accepted-insert":
					release := f.holdBlocked()
					f.set("transaction_timeout", "250ms")
					o := f.blocked("insert", "INSERT INTO timeout_blocked VALUES(8)", nil)
					f.diagnostic(o.Err, "25P04", "direct", "13")
					f.dead()
					release()
					if err := f.owner.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, f.peer, b.Recorder, fixture.Select("SELECT v FROM timeout_blocked", []string{"v"}, []string{"INT4"}, nil))
				case "healthy-statement":
					release := f.holdBlocked()
					f.set("statement_timeout", "125ms")
					o := f.blocked("direct", "SELECT v FROM timeout_blocked", nil)
					f.diagnostic(o.Err, "57014")
					f.show("statement_timeout", "125ms")
					release()
					if settingsPhysical(t, f.owner) != identity {
						t.Fatal("ordinary statement cancellation replaced owner")
					}
					if !b.PG {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						_, err := f.owner.ExecContext(ctx, "INSERT INTO timeout_sentinel VALUES(99)")
						if !errors.Is(err, context.Canceled) {
							t.Fatal("caller cancellation identity", err)
						}
						if settingsPhysical(t, f.owner) != identity {
							t.Fatal("caller cancellation replaced healthy owner")
						}
						f.show("statement_timeout", "125ms")
					}
					f.rows(f.peer, 7)
					return
				}
				fresh, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				if settingsPhysical(t, fresh) == identity {
					t.Fatal("dead pooled owner reused")
				}
				for _, name := range []string{"statement_timeout", "transaction_timeout", "lock_timeout", "idle_session_timeout", "idle_in_transaction_session_timeout"} {
					fixture.RunStep(t, p.Ctx, fresh, b.Recorder, localReferenceSelect(b.PG, "SHOW "+name, name, "0"))
				}
				f.rows(fresh, 7)
				f.rows(f.peer, 7)
			})
		})
	}
}

func TestReferenceTimeoutFeatureSmoke(t *testing.T) {
	timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		f := newTimeoutReferenceFixture(t, p, b)
		b.DB.SetMaxOpenConns(2)
		b.DB.SetMaxIdleConns(1)
		// One coherent committed baseline survives all four kinds of timeout.
		held, err := f.owner.PrepareContext(p.Ctx, "SELECT v FROM timeout_blocked")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { held.Close() })
		release := f.holdBlocked()
		f.set("lock_timeout", "125ms")
		identity := settingsPhysical(t, f.owner)
		o := f.blocked("held", "SELECT v FROM timeout_blocked", held)
		f.diagnostic(o.Err, "55P03", "held")
		f.set("lock_timeout", "0")
		release()
		if settingsPhysical(t, f.owner) != identity {
			t.Fatal("lock ERROR terminated owner")
		}
		f.rows(f.owner, 7)

		release = f.holdBlocked()
		f.set("transaction_timeout", "250ms")
		f.beginPrivate()
		o = f.blocked("insert", "INSERT INTO timeout_blocked VALUES(8)", nil)
		f.diagnostic(o.Err, "25P04", "direct", "13")
		f.dead()
		release()
		f.rows(f.peer, 7)
		fixture.RunStep(t, p.Ctx, f.peer, b.Recorder, fixture.Select("SELECT v FROM timeout_blocked", []string{"v"}, []string{"INT4"}, nil))
		if err := f.owner.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Fatal(err)
		}

		borrow := func() {
			t.Helper()
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { fresh.Close() })
			if settingsPhysical(t, fresh) == identity {
				t.Fatal("fatal pool owner was not replaced")
			}
			f.owner = fresh
			f.captureOwner()
			identity = settingsPhysical(t, fresh)
			for _, name := range []string{"transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout", "lock_timeout"} {
				f.show(name, "0")
			}
			f.rows(f.owner, 7)
		}
		borrow()
		f.set("idle_in_transaction_session_timeout", "250ms")
		f.beginPrivate()
		f.awaitIdleFatal("25P03", 250*time.Millisecond)
		f.rows(f.peer, 7)
		if err := f.owner.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Fatal(err)
		}

		borrow()
		f.set("idle_session_timeout", "250ms")
		f.awaitIdleFatal("57P05", 250*time.Millisecond)
		f.rows(f.peer, 7)
		if err := f.owner.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Fatal(err)
		}
		borrow()
		f.exec(f.owner, "INSERT INTO timeout_sentinel VALUES(9)", 1)
		f.rows(f.peer, 7, 9)
	})
}
