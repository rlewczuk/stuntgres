package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
)

type fdwUnitTransaction struct {
	calls       []string
	commitError error
	order       *[]string
	name        string
	check       func()
}

func (p *fdwUnitTransaction) record(call string) error {
	if p.check != nil {
		p.check()
	}
	p.calls = append(p.calls, call)
	return nil
}
func (p *fdwUnitTransaction) BeginStatement(context.Context) error {
	return p.record("begin-statement")
}
func (p *fdwUnitTransaction) EndStatement(_ context.Context, success bool) error {
	if success {
		return p.record("statement-success")
	}
	return p.record("statement-failure")
}
func (p *fdwUnitTransaction) Commit(context.Context) error {
	p.record("commit")
	if p.order != nil {
		*p.order = append(*p.order, p.name)
	}
	return p.commitError
}
func (p *fdwUnitTransaction) Rollback(context.Context) error { return p.record("rollback") }
func (p *fdwUnitTransaction) Savepoint(_ context.Context, name string) error {
	return p.record("save:" + name)
}
func (p *fdwUnitTransaction) RollbackTo(_ context.Context, name string) error {
	return p.record("rollback:" + name)
}
func (p *fdwUnitTransaction) Release(_ context.Context, name string) error {
	return p.record("release:" + name)
}

func TestFDWTransactionParticipants(t *testing.T) {
	t.Run("ChainReadmission", fdwChainReadmission)
	t.Run("HoldSettlement", fdwHoldSettlement)
	e := New()
	sess := sessionStart(t, e, nil)
	op := transactionOperation(t, sess)
	defer op.Done()
	ref, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	s.txRef = ref
	defer s.Finish(errors.New("unit cleanup"))
	p := &fdwUnitLifecycle{tx: &fdwUnitTransaction{}}
	p.tx.check = func() {
		if !e.mu.TryLock() {
			t.Error("transaction callback holds Engine.mu")
		} else {
			e.mu.Unlock()
		}
		if !s.owner.Mu.TryLock() {
			t.Error("transaction callback holds owner.mu")
		} else {
			s.owner.Mu.Unlock()
		}
		if !sess.Mu.TryLock() {
			t.Error("transaction callback holds Session.mu")
		} else {
			sess.Mu.Unlock()
		}
		if !e.replacementMu.TryLock() {
			t.Error("transaction callback holds replacement reservation")
		} else {
			e.replacementMu.Unlock()
		}
	}
	r := catalog.ForeignRequest{HandlerOID: 1, ServerOID: 2, MappingOID: 3, RoleOID: 4}
	first, err := s.enlistForeign(op.Context(), p, r, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.enlistForeign(op.Context(), p, r, false)
	foreign := ownerExtensionOrZero(s.owner).foreign
	if shared := foreign.Participant(foreignParticipantKey{Handler: 1, Server: 2, Mapping: 3, Role: 4}); err != nil || first != second || shared == nil || shared.Tx != first {
		t.Fatal("participant not shared", err)
	}
	if !reflect.DeepEqual(p.tx.calls, []string{"begin-statement"}) {
		t.Fatal(p.tx.calls)
	}
	if err := s.finishForeignStatements(nil); err != nil {
		t.Fatal(err)
	}
	if err := foreignSavepoint(s.owner, op.Context(), 0, 7); err != nil {
		t.Fatal(err)
	}
	if err := foreignSavepoint(s.owner, op.Context(), 1, 7); err != nil {
		t.Fatal(err)
	}
	if err := foreignSavepoint(s.owner, op.Context(), 2, 7); err != nil {
		t.Fatal(err)
	}
	if err := commitForeign(s.owner, op.Context(), ref); err != nil {
		t.Fatal(err)
	}
	ownerExtensionOf(s.owner).retireForeign()
	if err := RollbackForeign(op.Context(), ownerExtensionOf(s.owner).foreign.TakeCleanup()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.tx.calls, []string{"begin-statement", "statement-success", "save:stuntgres_sql_7", "rollback:stuntgres_sql_7", "release:stuntgres_sql_7", "commit"}) {
		t.Fatal("commit was rolled back/retried", p.tx.calls)
	}
	failure := errors.New("second participant commit uncertain")
	var order []string
	a := &fdwUnitTransaction{name: "a", order: &order}
	b := &fdwUnitTransaction{name: "b", order: &order, commitError: failure}
	c := &fdwUnitTransaction{name: "c", order: &order}
	for _, tx := range []*fdwUnitTransaction{a, b, c} {
		ownerExtensionOf(s.owner).foreign.Enlist(&foreignParticipant{Tx: tx})
	}
	if err := commitForeign(s.owner, op.Context(), ref); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"a", "b"}) || !reflect.DeepEqual(a.calls, []string{"commit"}) || !reflect.DeepEqual(b.calls, []string{"commit"}) || !reflect.DeepEqual(c.calls, []string{"rollback"}) {
		t.Fatal(order, a.calls, b.calls, c.calls)
	}
	_ = s.Finish(failure)
}
