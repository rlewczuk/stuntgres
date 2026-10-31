package locking_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestAdvisoryXactLockSnapshot(t *testing.T) {
	e := engine.New()
	a, _ := advisoryUnitOwner(t, e)
	b, _ := advisoryUnitOwner(t, e)
	key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryPairResource, High: 0xffffffff, Low: 2}
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, false)
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, false)
	advisoryXactUnitGrant(t, a, key, lock.AdvisoryShare, true)
	capture := func() []lock.Record {
		t.Helper()
		records, err := e.Locks.PGLocks(e.Catalog.Domain, e.CatalogRead().Epoch())
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
	request, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, key, lock.AdvisoryExclusive, lock.Policy{})
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
	e.EndLockOwner(a.Transactions.Ticket)
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
	enginetest.TransactionBegin(t, b)
	records, err := e.Locks.PGLocks(e.Catalog.Domain, e.CatalogRead().Epoch())
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
	enginetest.TransactionEnd(t, a, a.Transactions.Ref, false)
	if err := preparedUnitControl(t, e, a, parser.KindRollbackPrepared, "advisory_snapshot"); err != nil {
		t.Fatal(err)
	}
}

func advisoryXactUnitGrant(t *testing.T, s *session.Session, key lock.Resource, mode lock.Mode, xact bool) {
	t.Helper()
	r, err := advisoryXactUnitAcquire(t, s, key, mode, xact, true)
	if r != nil || err != nil {
		t.Fatal("expected immediate grant", r, err)
	}
}

func preparedUnitControl(t *testing.T, e *core.Engine, s *session.Session, kind parser.TransactionControlKind, gid string) error {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Execute(&parser.TransactionControl{Kind: kind, GID: gid}, 0, nil)
	finish := scope.Finish(err)
	if finish != err || scope.Finish(nil) != err {
		t.Fatal("control Finish replaced its execution diagnostic", err, finish)
	}
	return finish
}

func advisoryXactUnitAcquire(t *testing.T, s *session.Session, key lock.Resource, mode lock.Mode, xact, nowait bool) (*lock.Request, error) {
	t.Helper()
	return core.EngineOf(s).Locks.StartAcquire(context.Background(), advisoryXactUnitTicket(s, xact), key, mode, lock.Policy{Provenance: s.Transactions.Ref, NoWait: nowait})
}

func advisoryXactUnitTicket(s *session.Session, xact bool) *lock.Ticket {
	if xact {
		return s.Transactions.Ticket
	}
	return s.Advisory.Ticket
}
