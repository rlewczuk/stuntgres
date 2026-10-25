package indexcmds_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestBtreeIndexOrdinaryBuildAdmission(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			writer, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
			enginetest.SessionSQL(t, writer, "INSERT INTO t VALUES(1)")
			tx := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "INSERT INTO t VALUES(2)")
			done := enginetest.LockAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false)
			enginetest.LockPending(t, e, 1)
			// The queued ordinary utility must request exactly Share, not AX/SUE.
			lockState := e.Locks.Inspect()
			for _, entry := range lockState.Resources {
				for _, pending := range entry.Pending {
					if pending.Mode != lock.Share {
						t.Fatal("wrong build mode", pending.Mode)
					}
				}
			}
			enginetest.TransactionEnd(t, writer, tx, commit)
			enginetest.LockAwait(t, done, nil)
			if commit {
				btreeWantKeys(t, e, "idx", "1", "2")
			} else {
				btreeWantKeys(t, e, "idx", "1")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestBtreeIndexCompatibleReadersAndShare(t *testing.T) {
	for _, mode := range []string{"ACCESS SHARE", "ROW SHARE", "SHARE"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			reader, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, reader, "CREATE TABLE t(n INT)")
			tx := enginetest.TransactionBegin(t, reader)
			enginetest.SessionSQL(t, reader, "LOCK TABLE t IN "+mode+" MODE")
			enginetest.LockAwait(t, enginetest.LockAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false), nil)
			enginetest.TransactionEnd(t, reader, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestBtreeIndexWaitingCancellationAndEpochFence(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "reset"}[reset], func(t *testing.T) {
			e := engine.New()
			writer, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
			tx := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, builder, ctx, "CREATE INDEX idx ON t(n)", false)
			enginetest.LockPending(t, e, 1)
			if reset {
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
			} else {
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
			}
			enginetest.TransactionEnd(t, writer, tx, false)
			if len(e.Storage.IndexRoots) != 0 {
				t.Fatal("canceled/fenced build leaked roots")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestBtreeIndexBuildTimeout(t *testing.T) {
	e := engine.New()
	writer, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
	tx := enginetest.TransactionBegin(t, writer)
	enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	enginetest.SessionSQL(t, builder, "SET lock_timeout='20ms'")
	done := waitsCommandAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false)
	select {
	case err := <-done:
		enginetest.State(t, err, "55P03")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout did not terminate")
	}
	enginetest.TransactionEnd(t, writer, tx, false)
	if len(e.Storage.IndexRoots) != 0 {
		t.Fatal("timeout published root")
	}
	enginetest.LockEmpty(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := enginetest.Execute(e, ctx, mustLockStatement("CREATE INDEX idx ON t(n)"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBtreeIndexGrantTimeCompatibleRevision(t *testing.T) {
	e := engine.New()
	writer, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, writer, "INSERT INTO t VALUES(1)")
	tx := enginetest.TransactionBegin(t, writer)
	enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	done := enginetest.LockAsync(e, builder, context.Background(), "CREATE INDEX idx ON t(n)", false)
	enginetest.LockPending(t, e, 1)
	base := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "t")
	old := base.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, writer, tx, false)
	enginetest.LockAwait(t, done, nil)
	btreeWantKeys(t, e, "idx", "1")
	enginetest.LockEmpty(t, e)
}

func TestBtreeIndexConcurrentOrdinaryBuilds(t *testing.T) {
	e := engine.New()
	writer, a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, writer, "INSERT INTO t VALUES(1),(1),(NULL)")
	tx := enginetest.TransactionBegin(t, writer)
	enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	first := enginetest.LockAsync(e, a, context.Background(), "CREATE INDEX ON t(n)", false)
	second := enginetest.LockAsync(e, b, context.Background(), "CREATE INDEX ON t(n)", false)
	enginetest.LockPending(t, e, 2)
	enginetest.TransactionEnd(t, writer, tx, false)
	enginetest.LockAwait(t, first, nil)
	enginetest.LockAwait(t, second, nil)
	btreeWantKeys(t, e, "t_n_idx", "1", "1", "NULL")
	btreeWantKeys(t, e, "t_n_idx1", "1", "1", "NULL")
	enginetest.LockEmpty(t, e)
}

func TestBtreeIndexIndependentParentPublications(t *testing.T) {
	e := engine.New()
	setup, a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(n INT)", "CREATE TABLE b(n INT)", "CREATE INDEX ai ON a(n)", "CREATE INDEX bi ON b(n)"} {
		enginetest.SessionSQL(t, setup, q)
	}
	for range 32 {
		first := enginetest.LockAsync(e, a, context.Background(), "INSERT INTO a VALUES(1)", false)
		second := enginetest.LockAsync(e, b, context.Background(), "INSERT INTO b VALUES(2)", false)
		enginetest.LockAwait(t, first, nil)
		enginetest.LockAwait(t, second, nil)
	}
	_, left := btreeRoot(t, e, "ai")
	_, right := btreeRoot(t, e, "bi")
	if len(left.Entries) != 32 || len(right.Entries) != 32 {
		t.Fatal(len(left.Entries), len(right.Entries))
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
	enginetest.LockEmpty(t, e)
}
