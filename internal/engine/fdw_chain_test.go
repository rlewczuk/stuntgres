package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE chain_local(v integer)")
	sessionSQL(t, session, "BEGIN")
	sessionSQL(t, session, "SET application_name='committed-old'")
	sessionSQL(t, session, "INSERT INTO chain_local VALUES(7)")
	sessionSQL(t, session, "DECLARE chain_hold NO SCROLL CURSOR WITH HOLD FOR SELECT v FROM chain_local")
	promoted := session.cursors.entries["chain_hold"]
	owner := session.transactions
	ref := owner.ref
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "chain_local"})
	entered := make(chan struct{})
	held := false
	remote := &fdwChainTransaction{}
	remote.commit = func() error {
		held = e.replacementMu.TryLock()
		close(entered)
		if !held {
			return errors.New("provider Commit ran under replacement reservation")
		}
		return nil
	}
	owner.foreign.participants = []*foreignParticipant{{tx: remote}}
	op := transactionOperation(t, session)
	if err := owner.acquireAdmission(op.Context(), op); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result EndResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := e.endAdmitted(op, owner, ref, true, true, true, time.Now(), nil)
		owner.admission.Unlock()
		op.Done()
		done <- outcome{result, err}
	}()
	consumed := false
	defer func() {
		if held {
			e.replacementMu.Unlock()
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
		e.mu.Lock()
		published := len(e.storage.Relations[address].Versions) == 1
		if published {
			next := catalog.CloneState(e.catalog.state)
			next.SetEpoch(e.catalog.state.Epoch() + 1)
			e.catalog.state = next
		}
		e.mu.Unlock()
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
	e.replacementMu.Unlock()
	held = false
	got := <-done
	consumed = true
	if !errors.Is(got.err, ids.ErrCatalogReplaced) || !got.result.Committed || owner.state != TxIdle || owner.ref != (ids.TxRef{}) || owner.ticket != nil {
		t.Fatal("CHAIN changed old commit truth or installed stale next owner", got.result, got.err, owner.state)
	}
	if len(remote.calls) != 1 || remote.calls[0] != "commit" {
		t.Fatal("remote commit retried/compensated", remote.calls)
	}
	if session.cursors.entries["chain_hold"] != promoted || promoted == nil || !promoted.promoted || promoted.count != 1 {
		t.Fatal("CHAIN readmission failure discarded a committed HOLD promotion")
	}
	session.settings.mu.Lock()
	application := session.settings.value("application_name")
	session.settings.mu.Unlock()
	if application != "committed-old" {
		t.Fatal("old committed settings were rolled back", application)
	}
}
