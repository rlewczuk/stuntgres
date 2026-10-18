package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAdvisoryXactPreparedOwnership(t *testing.T) {
	for _, sessionMode := range []relationLockMode{advisoryShared, advisoryExclusive} {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		peer := sessionStart(t, e, nil)
		key := lockResource{kind: advisoryBigintResource, high: 1, low: 2}
		mark := e.locks.checkpoint(a.transactions.ticket)
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, key, sessionMode, false)
		err := e.locks.checkPreparedAdvisory(a.transactions.ticket)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "cannot PREPARE while holding both session-level and transaction-level locks on the same object" {
			t.Fatal("mixed actual tags", err)
		}
		// A rolled-back transaction contribution is absent even when the persistent
		// hold and its genuine resource journal still exist.
		e.locks.restore(a.transactions.ticket, mark)
		if err := e.locks.checkPreparedAdvisory(a.transactions.ticket); err != nil {
			t.Fatal("restriction on journal rather than grant", err)
		}
		pair := key
		pair.kind = advisoryPairResource
		advisoryXactUnitGrant(t, a, pair, advisoryExclusive, true)
		old := a.transactions.ticket
		if err := preparedUnitControl(t, e, a, parser.KindPrepareTransaction, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		if e.prepared["advisory_xact_unit"].ticket != old || !old.detached || old.participant() == a.advisory.ticket.participant() {
			t.Fatal("prepared owner did not detach")
		}
		transactionBegin(t, a)
		if _, err := advisoryXactUnitAcquire(t, a, pair, advisoryExclusive, true, true); !errors.Is(err, errRelationUnavailable) {
			t.Fatal("origin bypassed detached holder", err)
		}
		if advisoryUnitHold(a, key).mode != sessionMode {
			t.Fatal("session contribution transferred")
		}
		a.RequestEnd(CleanClose)
		if err := a.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
		if e.locks.owners[old.ref] != old {
			t.Fatal("origin close released detached hold")
		}
		if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		if e.locks.owners[old.ref] != nil || len(old.resources) != 0 {
			t.Fatal("completion leaked exact prepared owner")
		}
	}
}

func TestAdvisoryXactLockSnapshot(t *testing.T) {
	e := New()
	a, _ := advisoryUnitOwner(t, e)
	b, _ := advisoryUnitOwner(t, e)
	key := lockResource{kind: advisoryPairResource, high: 0xffffffff, low: 2}
	advisoryXactUnitGrant(t, a, key, advisoryShared, false)
	advisoryXactUnitGrant(t, a, key, advisoryShared, false)
	advisoryXactUnitGrant(t, a, key, advisoryShared, true)
	capture := func() []pgLockRecord {
		t.Helper()
		records, err := e.locks.snapshotPGLocks(e.CatalogRead().Epoch())
		if err != nil {
			t.Fatal(err)
		}
		var out []pgLockRecord
		for _, r := range records {
			if r.kind == pgAdvisoryLock && r.class == key.high && r.object == key.low && r.subobject == 2 {
				out = append(out, r)
			}
		}
		return out
	}
	rows := capture()
	if len(rows) != 1 || rows[0].mode != advisoryShared || rows[0].owner.local != a.transactions.ref.ID() {
		t.Fatal("same process mode duplication", rows)
	}
	advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
	rows = capture()
	if len(rows) != 1 || rows[0].mode != advisoryShared|advisoryExclusive {
		t.Fatal("combined mode record", rows)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := e.locks.startAcquire(ctx, b.transactions.ticket, key, advisoryExclusive, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	for range 2 {
		rows = capture()
		pending := 0
		for _, r := range rows {
			if r.waiting {
				pending++
				if r.owner.session != b.token.ID() || r.waitstart.IsZero() || r.mode != advisoryExclusive {
					t.Fatal("pending identity", r)
				}
			}
		}
		if len(rows) != 2 || pending != 1 || request.terminal {
			t.Fatal("capture mutated/merged pending request", rows)
		}
	}
	cancel()
	if err := request.wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	e.endLockOwner(a.transactions.ticket)
	rows = capture()
	if len(rows) != 1 || rows[0].mode != advisoryShared {
		t.Fatal("transaction end crossed session mode", rows)
	}
	// A different tag may be detached while the live origin owns its session tag.
	other := key
	other.low++
	advisoryXactUnitGrant(t, b, other, advisoryExclusive, true)
	old := b.transactions.ticket
	if err := preparedUnitControl(t, e, b, parser.KindPrepareTransaction, "advisory_snapshot"); err != nil {
		t.Fatal(err)
	}
	transactionBegin(t, b)
	records, err := e.locks.snapshotPGLocks(e.CatalogRead().Epoch())
	if err != nil {
		t.Fatal(err)
	}
	detached := 0
	for _, r := range records {
		if r.kind == pgAdvisoryLock && r.object == other.low && r.class == other.high {
			detached++
			if !r.owner.prepared || r.owner.local != old.ref.ID() {
				t.Fatal("detached capture lost origin", r)
			}
		}
	}
	if detached != 1 {
		t.Fatal("prepared row count", detached)
	}
	transactionEnd(t, a, a.transactions.ref, false)
	if err := preparedUnitControl(t, e, a, parser.KindRollbackPrepared, "advisory_snapshot"); err != nil {
		t.Fatal(err)
	}
}
