package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/fdw"
	"testing"
	"time"
)

func fdwHoldSettlement(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE hold_local(v integer)")
	sessionSQL(t, session, "INSERT INTO hold_local VALUES(1),(2)")
	sessionSQL(t, session, "BEGIN")
	sessionSQL(t, session, "DECLARE hold_owned NO SCROLL CURSOR WITH HOLD FOR SELECT v FROM hold_local")
	op := transactionOperation(t, session)
	defer op.Done()
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Finish(errors.New("unit cleanup"))
	s.Owner.Mu.Lock()
	ScopeAttachTransactionLocked(s)
	s.Owner.Mu.Unlock()
	remote := &fdwUnitTransaction{}
	participant := &fdw.Participant{Tx: remote}
	OwnerExtensionForeignRef(OwnerExtensionOf(s.Owner)).Enlist(participant)
	marks := []*fdw.Participant{participant}
	SetForeignTableScopeStatements(ScopeForeignTables(s), marks)
	frontier := e.Visibility.CommitSeq()
	if err := EngineHoldPrecommit(e, op, s.Owner, time.Now(), s); err != nil {
		t.Fatal(err)
	}
	if s.Finished || len(ForeignTableScopeValueStatements(ScopeForeignTablesOrZero(s))) != 1 || &ForeignTableScopeStatements(ScopeForeignTables(s))[0] != &marks[0] || ForeignTableScopeValueStatements(ScopeForeignTablesOrZero(s))[0] != participant || len(remote.calls) != 0 || e.Visibility.CommitSeq() != frontier {
		t.Fatal("borrowed HOLD scope settled, copied, or lost the current statement marks", remote.calls)
	}
	if len(ForeignTableScopeHoldPromotions(ScopeForeignTablesOrZero(s))) != 1 || ForeignTableScopeHoldPromotions(ScopeForeignTablesOrZero(s))[0] != PayloadOf(session).Cursors.Entries["hold_owned"] || !CursorPromoted(ForeignTableScopeHoldPromotions(ScopeForeignTablesOrZero(s))[0]) {
		t.Fatal("completion did not retain exact tentative promotion ownership")
	}
	remote.check = func() {
		if !e.Mu.TryLock() {
			t.Error("HOLD settlement holds Engine.mu")
		} else {
			e.Mu.Unlock()
		}
		if !s.Owner.Mu.TryLock() {
			t.Error("HOLD settlement holds owner.mu")
		} else {
			s.Owner.Mu.Unlock()
		}
		if !e.ReplacementMu.TryLock() {
			t.Error("HOLD settlement holds replacement reservation")
		} else {
			e.ReplacementMu.Unlock()
		}
	}
	if err := ScopeFinishForeignStatements(s, nil); err != nil {
		t.Fatal(err)
	}
	if len(ForeignTableScopeValueStatements(ScopeForeignTablesOrZero(s))) != 0 || !reflect.DeepEqual(remote.calls, []string{"statement-success"}) {
		t.Fatal("current completion did not settle transferred marks exactly once", remote.calls)
	}
	if err := ScopeDiscardForeignHoldPromotions(s, nil); err != nil {
		t.Fatal(err)
	}
	if PayloadOf(session).Cursors.Entries["hold_owned"] != nil || len(ForeignTableScopeHoldPromotions(ScopeForeignTablesOrZero(s))) != 0 {
		t.Fatal("unpublished completion retained a fresh HOLD promotion")
	}
}
