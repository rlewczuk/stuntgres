package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAdvisoryXactPreparedOwnership(t *testing.T) {
	for _, sessionMode := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
		e := New()
		a, _ := advisoryUnitOwner(t, e)
		peer := sessionStart(t, e, nil)
		key := lock.Resource{Kind: lock.AdvisoryBigintResource, High: 1, Low: 2}
		mark := e.locks.Checkpoint(a.Transactions.Ticket)
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
		advisoryXactUnitGrant(t, a, key, sessionMode, false)
		err := e.locks.CheckPreparedAdvisory(a.Transactions.Ticket)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "cannot PREPARE while holding both session-level and transaction-level locks on the same object" {
			t.Fatal("mixed actual tags", err)
		}
		// A rolled-back transaction contribution is absent even when the persistent
		// hold and its genuine resource journal still exist.
		e.locks.Restore(a.Transactions.Ticket, mark)
		if err := e.locks.CheckPreparedAdvisory(a.Transactions.Ticket); err != nil {
			t.Fatal("restriction on journal rather than grant", err)
		}
		pair := key
		pair.Kind = lock.AdvisoryPairResource
		advisoryXactUnitGrant(t, a, pair, lock.AdvisoryExclusive, true)
		old := a.Transactions.Ticket
		if err := preparedUnitControl(t, e, a, parser.KindPrepareTransaction, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		lockState := e.locks.Inspect(old)
		if e.prepared["advisory_xact_unit"].ticket != old || !lockState.Detached[old] || lockState.Participant(old) == lockState.Participant(a.Advisory.Ticket) {
			t.Fatal("prepared owner did not detach")
		}
		transactionBegin(t, a)
		if _, err := advisoryXactUnitAcquire(t, a, pair, lock.AdvisoryExclusive, true, true); !errors.Is(err, lock.ErrUnavailable) {
			t.Fatal("origin bypassed detached holder", err)
		}
		if advisoryUnitHold(a, key).Mode != sessionMode {
			t.Fatal("session contribution transferred")
		}
		a.RequestEnd(session.CleanClose)
		if err := a.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
		if e.locks.TransactionTicket(old.Ref()) != old {
			t.Fatal("origin close released detached hold")
		}
		if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		if e.locks.TransactionTicket(old.Ref()) != nil || len(e.locks.Inspect(old).Journals[old]) != 0 {
			t.Fatal("completion leaked exact prepared owner")
		}
	}
}

func TestAdvisoryXactLockSnapshot(t *testing.T) {
	e := New()
	a, _ := advisoryUnitOwner(t, e)
	b, _ := advisoryUnitOwner(t, e)
	key := lock.Resource{Kind: lock.AdvisoryPairResource, High: 0xffffffff, Low: 2}
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, false)
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, false)
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, true)
	capture := func() []lock.Record {
		t.Helper()
		records, err := e.locks.PGLocks(e.CatalogRead().Epoch())
		if err != nil {
			t.Fatal(err)
		}
		var out []lock.Record
		for _, r := range records {
			if r.Kind == lock.AdvisoryRecord && r.Class == key.High && r.Object == key.Low && r.Subobject == 2 {
				out = append(out, r)
			}
		}
		return out
	}
	rows := capture()
	if len(rows) != 1 || rows[0].Mode != lock.AdvisoryShare || rows[0].Owner.Local != a.Transactions.Ref.ID() {
		t.Fatal("same process mode duplication", rows)
	}
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
	rows = capture()
	if len(rows) != 1 || rows[0].Mode != lock.AdvisoryShare|lock.AdvisoryExclusive {
		t.Fatal("combined mode record", rows)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := e.locks.StartAcquire(ctx, b.Transactions.Ticket, key, lock.AdvisoryExclusive, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	for range 2 {
		rows = capture()
		pending := 0
		for _, r := range rows {
			if r.Waiting {
				pending++
				if r.Owner.Session != b.SessionToken.ID() || r.WaitStart.IsZero() || r.Mode != lock.AdvisoryExclusive {
					t.Fatal("pending identity", r)
				}
			}
		}
		if len(rows) != 2 || pending != 1 || request.Terminal() {
			t.Fatal("capture mutated/merged pending request", rows)
		}
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	e.endLockOwner(a.Transactions.Ticket)
	rows = capture()
	if len(rows) != 1 || rows[0].Mode != lock.AdvisoryShare {
		t.Fatal("transaction end crossed session mode", rows)
	}
	// A different tag may be detached while the live origin owns its session tag.
	other := key
	other.Low++
	advisoryXactUnitGrant(t, b, other, lock.AdvisoryExclusive, true)
	old := b.Transactions.Ticket
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
		if r.Kind == lock.AdvisoryRecord && r.Object == other.Low && r.Class == other.High {
			detached++
			if !r.Owner.Prepared || r.Owner.Local != old.Ref().ID() {
				t.Fatal("detached capture lost origin", r)
			}
		}
	}
	if detached != 1 {
		t.Fatal("prepared row count", detached)
	}
	transactionEnd(t, a, a.Transactions.Ref, false)
	if err := preparedUnitControl(t, e, a, parser.KindRollbackPrepared, "advisory_snapshot"); err != nil {
		t.Fatal(err)
	}
}
