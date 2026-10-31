package core_test

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
	"stuntgres/internal/sqlstate"
)

func TestAdvisoryXactPreparedOwnership(t *testing.T) {
	for _, sessionMode := range []lock.Mode{lock.AdvisoryShare, lock.AdvisoryExclusive} {
		e := engine.New()
		a, _ := advisoryUnitOwner(t, e)
		peer := enginetest.StartSession(t, e, nil)
		key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, High: 1, Low: 2}
		mark := e.Locks.Checkpoint(a.Transactions.Ticket)
		advisoryXactUnitGrant(t, a, key, lock.AdvisoryExclusive, true)
		advisoryXactUnitGrant(t, a, key, sessionMode, false)
		err := e.Locks.CheckPreparedAdvisory(a.Transactions.Ticket)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "cannot PREPARE while holding both session-level and transaction-level locks on the same object" {
			t.Fatal("mixed actual tags", err)
		}
		// A rolled-back transaction contribution is absent even when the persistent
		// hold and its genuine resource journal still exist.
		e.Locks.Restore(a.Transactions.Ticket, mark)
		if err := e.Locks.CheckPreparedAdvisory(a.Transactions.Ticket); err != nil {
			t.Fatal("restriction on journal rather than grant", err)
		}
		pair := key
		pair.Kind = lock.AdvisoryPairResource
		advisoryXactUnitGrant(t, a, pair, lock.AdvisoryExclusive, true)
		old := a.Transactions.Ticket
		if err := preparedUnitControl(t, e, a, parser.KindPrepareTransaction, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		lockState := e.Locks.Inspect(old)
		if core.EnginePreparedRef(e).Lookup("advisory_xact_unit").Ticket != old || !lockState.Detached[old] || lockState.Participant(old) == lockState.Participant(a.Advisory.Ticket) {
			t.Fatal("prepared owner did not detach")
		}
		enginetest.TransactionBegin(t, a)
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
		if e.Locks.TransactionTicket(old.Ref()) != old {
			t.Fatal("origin close released detached hold")
		}
		if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "advisory_xact_unit"); err != nil {
			t.Fatal(err)
		}
		if e.Locks.TransactionTicket(old.Ref()) != nil || len(e.Locks.Inspect(old).Journals[old]) != 0 {
			t.Fatal("completion leaked exact prepared owner")
		}
	}
}
