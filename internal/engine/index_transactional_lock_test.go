package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func transactionalIndexAsync(t *testing.T, e *Engine, s *session.Session, q string) <-chan error {
	t.Helper()
	stmt := statement(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done, finished := make(chan error, 1), make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("index worker did not join")
		}
	})
	go func() {
		defer close(finished)
		op, err := s.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, stmt, 0, nil)
			op.Done()
		}
		done <- err
	}()
	return done
}

func TestTransactionalIndexLocks(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			writer, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE t(n INT)")
			sessionSQL(t, writer, "CREATE TABLE later(n INT)")
			wt := transactionBegin(t, writer)
			sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
			bt := transactionBegin(t, builder)
			ticket := builder.Transactions.Ticket
			done := transactionalIndexAsync(t, e, builder, "CREATE INDEX idx ON t(n)")
			lockPending(t, e, 1)
			ref := roleTableRef(t, e, "t")
			found := false
			for resource, state := range e.locks.Inspect().Resources {
				for _, request := range state.Pending {
					if request.Ticket == ticket {
						if resource.ObjectRef != ref || request.Mode != lock.Share {
							t.Fatal("wrong explicit build resource/mode", resource, request.Mode)
						}
						found = true
					}
				}
			}
			if !found {
				t.Fatal("builder ticket not pending")
			}
			transactionEnd(t, writer, wt, commit)
			lockAwait(t, done, nil)
			held := false
			for resource, state := range e.locks.Inspect().Resources {
				if resource.ObjectRef == ref && state.Holders[ticket].Mode == lock.Share {
					held = true
				}
			}
			if !held || len(e.storage.IndexRoots) != 0 {
				t.Fatal("builder failed to retain private Share")
			}
			sessionSQL(t, builder, "SAVEPOINT mark")
			sessionSQL(t, builder, "CREATE INDEX later_idx ON later(n)")
			laterDone := transactionalIndexAsync(t, e, writer, "INSERT INTO later VALUES(2)")
			lockPending(t, e, 1)
			sessionSQL(t, builder, "ROLLBACK TO mark")
			lockAwait(t, laterDone, nil)
			earlyDone := transactionalIndexAsync(t, e, writer, "INSERT INTO t VALUES(3)")
			lockPending(t, e, 1)
			for _, resource := range e.locks.Inspect().Resources {
				for _, request := range resource.Pending {
					if request.Mode != lock.RowExclusive {
						t.Fatal("wrong blocked writer mode", request.Mode)
					}
				}
			}
			sessionSQL(t, builder, "ROLLBACK TO mark")
			transactionEnd(t, builder, bt, commit)
			lockAwait(t, earlyDone, nil)
			if commit {
				btreeWantKeys(t, e, "idx", "1", "3")
			}
			lockEmpty(t, e)
		})
	}
}

func TestTransactionalIndexReplacementFences(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, queued := range []bool{false, true} {
			t.Run(map[bool]string{false: "reset", true: "restore"}[restore]+"/"+map[bool]string{false: "staged", true: "queued"}[queued], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(n INT)")
				old := roleTableRef(t, e, "t")
				bt := transactionBegin(t, b)
				var dump bytes.Buffer
				if err := e.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				var done <-chan error
				if queued {
					transactionBegin(t, a)
					sessionSQL(t, a, "INSERT INTO t VALUES(1)")
					done = transactionalIndexAsync(t, e, b, "CREATE INDEX idx ON t(n)")
					lockPending(t, e, 1)
				} else {
					sessionSQL(t, b, "CREATE INDEX idx ON t(n)")
					sessionSQL(t, b, "SAVEPOINT mark")
					prior, epoch := b.Transactions.Catalog, e.catalog.State.Epoch()
					if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE bad(n INT); INSERT INTO bad VALUES('bad');")); err == nil {
						t.Fatal("invalid Restore succeeded")
					}
					if b.Transactions.Catalog != prior || e.catalog.State.Epoch() != epoch {
						t.Fatal("failed replacement changed eligible journal/epoch")
					}
				}
				if restore {
					if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				if queued {
					lockAwait(t, done, ids.ErrCatalogReplaced)
				} else {
					op := transactionOperation(t, b)
					_, err := e.Execute(op, statement(t, "ROLLBACK TO mark"), 0, nil)
					op.Done()
					if err != ids.ErrCatalogReplaced {
						t.Fatal("stale rollback-to", err)
					}
				}
				transactionEnd(t, b, bt, false)
				sessionSQL(t, a, "ROLLBACK")
				if restore {
					sessionSQL(t, a, "DROP TABLE t")
				}
				sessionSQL(t, a, "CREATE TABLE t(n INT)")
				fresh := roleTableRef(t, e, "t")
				if fresh == old || len(e.storage.IndexRoots) != 0 {
					t.Fatal("replacement retargeted old build")
				}
				lockEmpty(t, e)
			})
		}
	}
}
