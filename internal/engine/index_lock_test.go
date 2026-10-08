package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBtreeIndexOrdinaryBuildAdmission(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			writer, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE t(n INT)")
			sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
			tx := transactionBegin(t, writer)
			sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
			done := lockAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false)
			lockPending(t, e, 1)
			// The queued ordinary utility must request exactly Share, not AX/SUE.
			e.locks.mu.Lock()
			for _, entry := range e.locks.entries {
				for _, pending := range entry.pending {
					if pending.mode != relationShare {
						e.locks.mu.Unlock()
						t.Fatal("wrong build mode", pending.mode)
					}
				}
			}
			e.locks.mu.Unlock()
			transactionEnd(t, writer, tx, commit)
			lockAwait(t, done, nil)
			if commit {
				btreeWantKeys(t, e, "idx", "1", "2")
			} else {
				btreeWantKeys(t, e, "idx", "1")
			}
			lockEmpty(t, e)
		})
	}
}
func TestBtreeIndexCompatibleReadersAndShare(t *testing.T) {
	for _, mode := range []string{"ACCESS SHARE", "ROW SHARE", "SHARE"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			reader, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, reader, "CREATE TABLE t(n INT)")
			tx := transactionBegin(t, reader)
			sessionSQL(t, reader, "LOCK TABLE t IN "+mode+" MODE")
			lockAwait(t, lockAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false), nil)
			transactionEnd(t, reader, tx, false)
			lockEmpty(t, e)
		})
	}
}
func TestBtreeIndexWaitingCancellationAndEpochFence(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "reset"}[reset], func(t *testing.T) {
			e := New()
			writer, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE t(n INT)")
			tx := transactionBegin(t, writer)
			sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, builder, ctx, "CREATE INDEX idx ON t(n)", false)
			lockPending(t, e, 1)
			if reset {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ErrCatalogReplaced)
			} else {
				cancel()
				lockAwait(t, done, context.Canceled)
			}
			transactionEnd(t, writer, tx, false)
			if len(e.indexRoots) != 0 {
				t.Fatal("canceled/fenced build leaked roots")
			}
			lockEmpty(t, e)
		})
	}
}
func TestBtreeIndexBuildTimeout(t *testing.T) {
	e := New()
	writer, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	tx := transactionBegin(t, writer)
	sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	sessionSQL(t, builder, "SET lock_timeout='20ms'")
	done := waitsCommandAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false)
	select {
	case err := <-done:
		state(t, err, "55P03")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout did not terminate")
	}
	transactionEnd(t, writer, tx, false)
	if len(e.indexRoots) != 0 {
		t.Fatal("timeout published root")
	}
	lockEmpty(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := testExecute(e, ctx, mustLockStatement("CREATE INDEX idx ON t(n)"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBtreeIndexGrantTimeCompatibleRevision(t *testing.T) {
	e := New()
	writer, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
	tx := transactionBegin(t, writer)
	sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	done := lockAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false)
	lockPending(t, e, 1)
	base := e.CatalogRead()
	ref := roleTableRef(t, e, "t")
	old := base.state.descriptors[ref.Address].(*relationDescriptor)
	next := *old
	next.revision++
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	transactionEnd(t, writer, tx, false)
	lockAwait(t, done, nil)
	btreeWantKeys(t, e, "idx", "1")
	lockEmpty(t, e)
}

func TestBtreeIndexConcurrentOrdinaryBuilds(t *testing.T) {
	e := New()
	writer, a, b := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1),(1),(NULL)")
	tx := transactionBegin(t, writer)
	sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	first := lockAsync(e, a, context.Background(), "CREATE INDEX ON t(n)", false)
	second := lockAsync(e, b, context.Background(), "CREATE INDEX ON t(n)", false)
	lockPending(t, e, 2)
	transactionEnd(t, writer, tx, false)
	lockAwait(t, first, nil)
	lockAwait(t, second, nil)
	btreeWantKeys(t, e, "t_n_idx", "1", "1", "NULL")
	btreeWantKeys(t, e, "t_n_idx1", "1", "1", "NULL")
	lockEmpty(t, e)
}

func TestBtreeIndexIndependentParentPublications(t *testing.T) {
	e := New()
	setup, a, b := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(n INT)", "CREATE TABLE b(n INT)", "CREATE INDEX ai ON a(n)", "CREATE INDEX bi ON b(n)"} {
		sessionSQL(t, setup, q)
	}
	for range 32 {
		first := lockAsync(e, a, context.Background(), "INSERT INTO a VALUES(1)", false)
		second := lockAsync(e, b, context.Background(), "INSERT INTO b VALUES(2)", false)
		lockAwait(t, first, nil)
		lockAwait(t, second, nil)
	}
	_, left := btreeRoot(t, e, "ai")
	_, right := btreeRoot(t, e, "bi")
	if len(left.entries) != 32 || len(right.entries) != 32 {
		t.Fatal(len(left.entries), len(right.entries))
	}
	for _, key := range btreeKeys(t, left) {
		if key != "1" {
			t.Fatal(key)
		}
	}
	for _, key := range btreeKeys(t, right) {
		if key != "2" {
			t.Fatal(key)
		}
	}
	lockEmpty(t, e)
}
