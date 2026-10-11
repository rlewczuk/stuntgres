package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Exercise the same typed role/identity journals without adding SQL capability.
func batchUnitIdentity(m *batchUnitMessage, current PrincipalRef) bool {
	s := m.op.session
	t := s.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	s.settings.mu.Lock()
	defer s.settings.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if roleRevisionHeadroom(s.authorization, uint64(len(t.savepoints))+3) != nil {
		return false
	}
	s.Engine().touchOperationIdentityLocked(m.op)
	s.settings.touchRole(s.authorization.state.CurrentUser, false)
	s.authorization.state.CurrentUser = current
	s.authorization.revision++
	return true
}
func TestDriverBatchIdentityPromotionMarksAndRevision(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE ROLE a", "CREATE ROLE b", "CREATE ROLE c"} {
		sessionSQL(t, s, q)
	}
	b, _ := e.Roles().Lookup("b")
	c, _ := e.Roles().Lookup("c")
	original := s.authorization.state
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	for _, q := range []string{"SET ROLE a", "BEGIN", "SAVEPOINT p"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(q, err)
		}
	}
	mark := s.authorization.state
	revision := s.authorization.revision
	ref := s.transactions.ref
	if err := m.end(nil); err != nil {
		t.Fatal(err)
	}
	if !s.transactions.identityCaptured || s.transactions.identityEntry != original || s.transactions.retainedMessage || s.transactions.ref != ref {
		t.Fatal("message reset lost promoted identity entry")
	}
	n := batchUnitStart(t, s, context.Background())
	defer n.end(context.Canceled)
	if !batchUnitIdentity(n, b.Ref) {
		t.Fatal("internal mutation capacity")
	}
	if _, err := n.step(statement(t, "SAVEPOINT q")); err != nil {
		t.Fatal(err)
	}
	if !batchUnitIdentity(n, c.Ref) {
		t.Fatal("nested mutation capacity")
	}
	if _, err := n.step(statement(t, "RELEASE q")); err != nil {
		t.Fatal(err)
	}
	if s.authorization.state.CurrentUser != c.Ref || len(s.transactions.savepoints) != 1 {
		t.Fatal("release rewound identity")
	}
	if _, err := n.step(statement(t, "ROLLBACK TO p")); err != nil {
		t.Fatal(err)
	}
	if s.authorization.state != mark || s.authorization.revision <= revision || s.transactions.identityEntry != original {
		t.Fatal("outer mark restoration")
	}
	if _, err := n.step(statement(t, "SAVEPOINT q")); err != nil {
		t.Fatal(err)
	}
	if !batchUnitIdentity(n, b.Ref) {
		t.Fatal("recovery mutation capacity")
	}
	revision = s.authorization.revision
	_, failure := n.step(statement(t, "SELECT missing"))
	if failure == nil {
		t.Fatal("admitted failure")
	}
	if s.TransactionState() != TxAborted || s.authorization.state != mark || s.authorization.revision <= revision {
		t.Fatal("abort did not restore reached mark identity monotonically")
	}
	revision = s.authorization.revision
	if _, err := n.step(statement(t, "ROLLBACK TO q")); err != nil {
		t.Fatal(err)
	}
	if s.TransactionState() != TxActive || s.authorization.state != mark || s.authorization.revision != revision {
		t.Fatal("same-state recovery invented mutation")
	}
	if _, err := n.step(statement(t, "ROLLBACK")); err != nil {
		t.Fatal(err)
	}
	if err := n.end(nil); err != nil {
		t.Fatal(err)
	}
	if s.authorization.state != original || s.authorization.revision <= revision || s.transactions.identityCaptured {
		t.Fatal("outer rollback lost entry identity")
	}
}
func TestDriverBatchIdentityRevisionCapacity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE a")
	role, _ := e.Roles().Lookup("a")
	original := s.authorization.state
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	s.authorization.revision = ^uint64(0) - 3
	if !batchUnitIdentity(m, role.Ref) || s.authorization.revision != ^uint64(0)-2 {
		t.Fatal("reserved mutation denied")
	}
	if batchUnitIdentity(m, role.Ref) {
		t.Fatal("rollback reservation consumed")
	}
	if _, err := m.step(statement(t, "BEGIN")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.step(statement(t, "ROLLBACK")); err != nil {
		t.Fatal(err)
	}
	if s.authorization.state != original || s.authorization.revision != ^uint64(0)-1 {
		t.Fatal("rollback revision wrapped")
	}
	_, err := m.step(statement(t, "SET ROLE a"))
	var diagnostic interface{ SQLState() string }
	if err == nil || !strings.Contains(err.Error(), "session authorization revision exhausted") || errors.As(err, &diagnostic) || s.authorization.state != original || s.authorization.revision != ^uint64(0)-1 {
		t.Fatal("exhaustion mutated or forged SQL diagnostic", err)
	}
	if end := m.end(err); end != err {
		t.Fatal(end)
	}
}
