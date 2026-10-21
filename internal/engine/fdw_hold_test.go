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
	s.owner.Mu.Lock()
	s.attachTransactionLocked()
	s.owner.Mu.Unlock()
	remote := &fdwUnitTransaction{}
	participant := &fdw.Participant{Tx: remote}
	ownerExtensionOf(s.owner).foreign.Enlist(participant)
	marks := []*fdw.Participant{participant}
	s.foreignTables().statements = marks
	frontier := e.visibility.CommitSeq()
	if err := e.holdPrecommit(op, s.owner, time.Now(), s); err != nil {
		t.Fatal(err)
	}
	if s.finished || len(s.foreignTablesOrZero().statements) != 1 || &s.foreignTables().statements[0] != &marks[0] || s.foreignTablesOrZero().statements[0] != participant || len(remote.calls) != 0 || e.visibility.CommitSeq() != frontier {
		t.Fatal("borrowed HOLD scope settled, copied, or lost the current statement marks", remote.calls)
	}
	if len(s.foreignTablesOrZero().holdPromotions) != 1 || s.foreignTablesOrZero().holdPromotions[0] != payloadOf(session).cursors.entries["hold_owned"] || !s.foreignTablesOrZero().holdPromotions[0].promoted {
		t.Fatal("completion did not retain exact tentative promotion ownership")
	}
	remote.check = func() {
		if !e.mu.TryLock() {
			t.Error("HOLD settlement holds Engine.mu")
		} else {
			e.mu.Unlock()
		}
		if !s.owner.Mu.TryLock() {
			t.Error("HOLD settlement holds owner.mu")
		} else {
			s.owner.Mu.Unlock()
		}
		if !e.replacementMu.TryLock() {
			t.Error("HOLD settlement holds replacement reservation")
		} else {
			e.replacementMu.Unlock()
		}
	}
	if err := s.finishForeignStatements(nil); err != nil {
		t.Fatal(err)
	}
	if len(s.foreignTablesOrZero().statements) != 0 || !reflect.DeepEqual(remote.calls, []string{"statement-success"}) {
		t.Fatal("current completion did not settle transferred marks exactly once", remote.calls)
	}
	if err := s.discardForeignHoldPromotions(nil); err != nil {
		t.Fatal(err)
	}
	if payloadOf(session).cursors.entries["hold_owned"] != nil || len(s.foreignTablesOrZero().holdPromotions) != 0 {
		t.Fatal("unpublished completion retained a fresh HOLD promotion")
	}
}
