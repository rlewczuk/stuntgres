package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

type fdwChainTransaction struct {
	fdwUnitTransaction
	commit func() error
}

func (p *fdwChainTransaction) Commit(context.Context) error { p.record("commit"); return p.commit() }

// A genuine local publication finishes while an external replacement writer
// holds admission for the next incarnation. The epoch fault is injected under
// that exact replacement lock, without a production hook or timing-only race.
func fdwChainReadmission(t *testing.T) {
	e := engine.New()
	sess := sessionStart(t, e, nil)
	sessionSQL(t, sess, "CREATE TABLE chain_local(v integer)")
	sessionSQL(t, sess, "BEGIN")
	sessionSQL(t, sess, "SET application_name='committed-old'")
	sessionSQL(t, sess, "INSERT INTO chain_local VALUES(7)")
	sessionSQL(t, sess, "DECLARE chain_hold NO SCROLL CURSOR WITH HOLD FOR SELECT v FROM chain_local")
	promoted := core.PayloadOf(sess).Cursors.Entries["chain_hold"]
	owner := sess.Transactions
	ref := owner.Ref
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "chain_local"})
	entered := make(chan struct{})
	held := false
	remote := &fdwChainTransaction{}
	remote.commit = func() error {
		held = e.ReplacementMu.TryLock()
		close(entered)
		if !held {
			return errors.New("provider Commit ran under replacement reservation")
		}
		return nil
	}
	core.OwnerExtensionForeignRef(core.OwnerExtensionOf(owner)).Enlist(&fdw.Participant{Tx: remote})
	op := transactionOperation(t, sess)
	if err := owner.AcquireAdmission(op.Context(), op); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result core.EndResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := core.EngineEndAdmitted(e, op, owner, ref, true, true, true, time.Now(), nil)
		owner.Admission.Unlock()
		op.Done()
		done <- outcome{result, err}
	}()
	consumed := false
	defer func() {
		if held {
			e.ReplacementMu.Unlock()
			held = false
		}
		if !consumed {
			<-done
		}
	}()
	select {
	case <-entered:
	case got := <-done:
		consumed = true
		t.Fatal("Commit not entered", got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("Commit admission blocked")
	}
	if !held {
		got := <-done
		consumed = true
		t.Fatal(got.err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	observation := time.NewTicker(time.Millisecond)
	defer observation.Stop()
	for {
		e.Mu.Lock()
		published := len(e.Storage.Relations[address].Versions) == 1
		if published {
			next := catalog.CloneState(e.Catalog.State)
			next.SetEpoch(e.Catalog.State.Epoch() + 1)
			e.Catalog.PublishLocked(next)
		}
		e.Mu.Unlock()
		if published {
			break
		}
		select {
		case got := <-done:
			consumed = true
			t.Fatal("old publication failed", got.err)
		case <-deadline.C:
			t.Fatal("old publication did not finish")
		case <-observation.C:
		}
	}
	e.ReplacementMu.Unlock()
	held = false
	got := <-done
	consumed = true
	if !errors.Is(got.err, ids.ErrCatalogReplaced) || !got.result.Committed || owner.State != session.TxIdle || owner.Ref != (ids.TxRef{}) || owner.Ticket != nil {
		t.Fatal("CHAIN changed old commit truth or installed stale next owner", got.result, got.err, owner.State)
	}
	if len(remote.calls) != 1 || remote.calls[0] != "commit" {
		t.Fatal("remote commit retried/compensated", remote.calls)
	}
	if core.PayloadOf(sess).Cursors.Entries["chain_hold"] != promoted || promoted == nil || !core.CursorPromoted(promoted) || core.CursorCount(promoted) != 1 {
		t.Fatal("CHAIN readmission failure discarded a committed HOLD promotion")
	}
	sess.Settings.Mu.Lock()
	application := sess.Settings.Value("application_name")
	sess.Settings.Mu.Unlock()
	if application != "committed-old" {
		t.Fatal("old committed settings were rolled back", application)
	}
}
