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
		key := lockResource{Kind: advisoryBigintResource, High: 1, Low: 2}
		mark := e.locks.Checkpoint(a.transactions.ticket)
		advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
		advisoryXactUnitGrant(t, a, key, sessionMode, false)
		err := e.locks.CheckPreparedAdvisory(a.transactions.ticket)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "cannot PREPARE while holding both session-level and transaction-level locks on the same object" {
			t.Fatal("mixed actual tags", err)
		}
		// A rolled-back transaction contribution is absent even when the persistent
		// hold and its genuine resource journal still exist.
		e.locks.Restore(a.transactions.ticket, mark)
		if err := e.locks.CheckPreparedAdvisory(a.transactions.ticket); err != nil {
			t.Fatal("restriction on journal rather than grant", err)
		}
		pair := key
		pair.Kind = advisoryPairResource
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
		if advisoryUnitHold(a, key).Mode != sessionMode {
			t.Fatal("session contribution transferred")
		}
		a.RequestEnd(CleanClose)
		if err := a.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
		if e.locks.owners[old.Ref()] != old {
			t.Fatal("origin close released detached hold")
		}
		if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		if e.locks.owners[old.Ref()] != nil || len(old.resources) != 0 {
			t.Fatal("completion leaked exact prepared owner")
		}
	}
}

func TestAdvisoryXactLockSnapshot(t *testing.T) {
	e := New()
	a, _ := advisoryUnitOwner(t, e)
	b, _ := advisoryUnitOwner(t, e)
	key := lockResource{Kind: advisoryPairResource, High: 0xffffffff, Low: 2}
	advisoryXactUnitGrant(t, a, key, advisoryShared, false)
	advisoryXactUnitGrant(t, a, key, advisoryShared, false)
	advisoryXactUnitGrant(t, a, key, advisoryShared, true)
	capture := func() []pgLockRecord {
		t.Helper()
		records, err := e.locks.PGLocks(e.CatalogRead().Epoch())
		if err != nil {
			t.Fatal(err)
		}
		var out []pgLockRecord
		for _, r := range records {
			if r.Kind == pgAdvisoryLock && r.Class == key.High && r.Object == key.Low && r.Subobject == 2 {
				out = append(out, r)
			}
		}
		return out
	}
	rows := capture()
	if len(rows) != 1 || rows[0].Mode != advisoryShared || rows[0].Owner.Local != a.transactions.ref.ID() {
		t.Fatal("same process mode duplication", rows)
	}
	advisoryXactUnitGrant(t, a, key, advisoryExclusive, true)
	rows = capture()
	if len(rows) != 1 || rows[0].Mode != advisoryShared|advisoryExclusive {
		t.Fatal("combined mode record", rows)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := e.locks.StartAcquire(ctx, b.transactions.ticket, key, advisoryExclusive, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	for range 2 {
		rows = capture()
		pending := 0
		for _, r := range rows {
			if r.Waiting {
				pending++
				if r.Owner.Session != b.token.ID() || r.WaitStart.IsZero() || r.Mode != advisoryExclusive {
					t.Fatal("pending identity", r)
				}
			}
		}
		if len(rows) != 2 || pending != 1 || request.terminal {
			t.Fatal("capture mutated/merged pending request", rows)
		}
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	e.endLockOwner(a.transactions.ticket)
	rows = capture()
	if len(rows) != 1 || rows[0].Mode != advisoryShared {
		t.Fatal("transaction end crossed session mode", rows)
	}
	// A different tag may be detached while the live origin owns its session tag.
	other := key
	other.Low++
	advisoryXactUnitGrant(t, b, other, advisoryExclusive, true)
	old := b.transactions.ticket
	if err := preparedUnitControl(t, e, b, parser.KindPrepareTransaction, "advisory_snapshot"); err != nil {
		t.Fatal(err)
	}
	transactionBegin(t, b)
	records, err := e.locks.PGLocks(e.CatalogRead().Epoch())
	if err != nil {
		t.Fatal(err)
	}
	detached := 0
	for _, r := range records {
		if r.Kind == pgAdvisoryLock && r.Object == other.Low && r.Class == other.High {
			detached++
			if !r.Owner.Prepared || r.Owner.Local != old.Ref().ID() {
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
