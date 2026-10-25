package indexcmds_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func transactionalIndexAsync(t *testing.T, e *engine.Engine, s *session.Session, q string) <-chan error {
	t.Helper()
	stmt := enginetest.Statement(t, q)
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
			e := engine.New()
			writer, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
			enginetest.SessionSQL(t, writer, "CREATE TABLE later(n INT)")
			wt := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "INSERT INTO t VALUES(1)")
			bt := enginetest.TransactionBegin(t, builder)
			ticket := builder.Transactions.Ticket
			done := transactionalIndexAsync(t, e, builder, "CREATE INDEX idx ON t(n)")
			enginetest.LockPending(t, e, 1)
			ref := enginetest.RoleTableRef(t, e, "t")
			found := false
			for resource, state := range e.Locks.Inspect().Resources {
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
			enginetest.TransactionEnd(t, writer, wt, commit)
			enginetest.LockAwait(t, done, nil)
			held := false
			for resource, state := range e.Locks.Inspect().Resources {
				if resource.ObjectRef == ref && state.Holders[ticket].Mode == lock.Share {
					held = true
				}
			}
			if !held || len(e.Storage.IndexRoots) != 0 {
				t.Fatal("builder failed to retain private Share")
			}
			enginetest.SessionSQL(t, builder, "SAVEPOINT mark")
			enginetest.SessionSQL(t, builder, "CREATE INDEX later_idx ON later(n)")
			laterDone := transactionalIndexAsync(t, e, writer, "INSERT INTO later VALUES(2)")
			enginetest.LockPending(t, e, 1)
			enginetest.SessionSQL(t, builder, "ROLLBACK TO mark")
			enginetest.LockAwait(t, laterDone, nil)
			earlyDone := transactionalIndexAsync(t, e, writer, "INSERT INTO t VALUES(3)")
			enginetest.LockPending(t, e, 1)
			for _, resource := range e.Locks.Inspect().Resources {
				for _, request := range resource.Pending {
					if request.Mode != lock.RowExclusive {
						t.Fatal("wrong blocked writer mode", request.Mode)
					}
				}
			}
			enginetest.SessionSQL(t, builder, "ROLLBACK TO mark")
			enginetest.TransactionEnd(t, builder, bt, commit)
			enginetest.LockAwait(t, earlyDone, nil)
			if commit {
				btreeWantKeys(t, e, "idx", "1", "3")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestTransactionalIndexReplacementFences(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, queued := range []bool{false, true} {
			t.Run(map[bool]string{false: "reset", true: "restore"}[restore]+"/"+map[bool]string{false: "staged", true: "queued"}[queued], func(t *testing.T) {
				e := engine.New()
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
				old := enginetest.RoleTableRef(t, e, "t")
				bt := enginetest.TransactionBegin(t, b)
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), e, &dump); err != nil {
					t.Fatal(err)
				}
				var done <-chan error
				if queued {
					enginetest.TransactionBegin(t, a)
					enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
					done = transactionalIndexAsync(t, e, b, "CREATE INDEX idx ON t(n)")
					enginetest.LockPending(t, e, 1)
				} else {
					enginetest.SessionSQL(t, b, "CREATE INDEX idx ON t(n)")
					enginetest.SessionSQL(t, b, "SAVEPOINT mark")
					prior, epoch := b.Transactions.Catalog, e.Catalog.State.Epoch()
					if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE bad(n INT); INSERT INTO bad VALUES('bad');")); err == nil {
						t.Fatal("invalid Restore succeeded")
					}
					if b.Transactions.Catalog != prior || e.Catalog.State.Epoch() != epoch {
						t.Fatal("failed replacement changed eligible journal/epoch")
					}
				}
				if restore {
					if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				}
				if queued {
					enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				} else {
					op := enginetest.TransactionOperation(t, b)
					_, err := e.Execute(op, enginetest.Statement(t, "ROLLBACK TO mark"), 0, nil)
					op.Done()
					if err != ids.ErrCatalogReplaced {
						t.Fatal("stale rollback-to", err)
					}
				}
				enginetest.TransactionEnd(t, b, bt, false)
				enginetest.SessionSQL(t, a, "ROLLBACK")
				if restore {
					enginetest.SessionSQL(t, a, "DROP TABLE t")
				}
				enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
				fresh := enginetest.RoleTableRef(t, e, "t")
				if fresh == old || len(e.Storage.IndexRoots) != 0 {
					t.Fatal("replacement retargeted old build")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
