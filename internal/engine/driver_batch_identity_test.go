package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"testing"
)

// Exercise the same typed role/identity journals without adding SQL capability.
func batchUnitIdentity(m *batchUnitMessage, current authorization.PrincipalRef) bool {
	s := m.op.Session
	t := s.Transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	s.Settings.mu.Lock()
	defer s.Settings.mu.Unlock()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if roleRevisionHeadroom(s.Authorization, uint64(len(t.savepoints))+3) != nil {
		return false
	}
	engineOf(s).touchOperationIdentityLocked(m.op)
	s.Settings.touchRole(s.Authorization.state.CurrentUser, false)
	s.Authorization.state.CurrentUser = current
	s.Authorization.revision++
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
	original := s.Authorization.state
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	for _, q := range []string{"SET ROLE a", "BEGIN", "SAVEPOINT p"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(q, err)
		}
	}
	mark := s.Authorization.state
	revision := s.Authorization.revision
	ref := s.Transactions.ref
	if err := m.end(nil); err != nil {
		t.Fatal(err)
	}
	if !s.Transactions.identityCaptured || s.Transactions.identityEntry != original || s.Transactions.retainedMessage || s.Transactions.ref != ref {
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
	if s.Authorization.state.CurrentUser != c.Ref || len(s.Transactions.savepoints) != 1 {
		t.Fatal("release rewound identity")
	}
	if _, err := n.step(statement(t, "ROLLBACK TO p")); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.state != mark || s.Authorization.revision <= revision || s.Transactions.identityEntry != original {
		t.Fatal("outer mark restoration")
	}
	if _, err := n.step(statement(t, "SAVEPOINT q")); err != nil {
		t.Fatal(err)
	}
	if !batchUnitIdentity(n, b.Ref) {
		t.Fatal("recovery mutation capacity")
	}
	revision = s.Authorization.revision
	_, failure := n.step(statement(t, "SELECT missing"))
	if failure == nil {
		t.Fatal("admitted failure")
	}
	if s.TransactionState() != TxAborted || s.Authorization.state != mark || s.Authorization.revision <= revision {
		t.Fatal("abort did not restore reached mark identity monotonically")
	}
	revision = s.Authorization.revision
	if _, err := n.step(statement(t, "ROLLBACK TO q")); err != nil {
		t.Fatal(err)
	}
	if s.TransactionState() != TxActive || s.Authorization.state != mark || s.Authorization.revision != revision {
		t.Fatal("same-state recovery invented mutation")
	}
	if _, err := n.step(statement(t, "ROLLBACK")); err != nil {
		t.Fatal(err)
	}
	if err := n.end(nil); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.state != original || s.Authorization.revision <= revision || s.Transactions.identityCaptured {
		t.Fatal("outer rollback lost entry identity")
	}
}
func TestDriverBatchIdentityRevisionCapacity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE a")
	role, _ := e.Roles().Lookup("a")
	original := s.Authorization.state
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	s.Authorization.revision = ^uint64(0) - 3
	if !batchUnitIdentity(m, role.Ref) || s.Authorization.revision != ^uint64(0)-2 {
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
	if s.Authorization.state != original || s.Authorization.revision != ^uint64(0)-1 {
		t.Fatal("rollback revision wrapped")
	}
	_, err := m.step(statement(t, "SET ROLE a"))
	var diagnostic interface{ SQLState() string }
	if err == nil || !strings.Contains(err.Error(), "session authorization revision exhausted") || errors.As(err, &diagnostic) || s.Authorization.state != original || s.Authorization.revision != ^uint64(0)-1 {
		t.Fatal("exhaustion mutated or forged SQL diagnostic", err)
	}
	if end := m.end(err); end != err {
		t.Fatal(end)
	}
}
