package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"testing"
)

// Exercise the same typed role/identity journals without adding SQL capability.
func batchUnitIdentity(m *batchUnitMessage, current authorization.PrincipalRef) bool {
	s := m.op.Session
	t := s.Transactions
	t.Mu.Lock()
	defer t.Mu.Unlock()
	s.Settings.Mu.Lock()
	defer s.Settings.Mu.Unlock()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if session.RoleRevisionHeadroom(s.Authorization, uint64(len(t.Savepoints))+3) != nil {
		return false
	}
	engineOf(s).touchOperationIdentityLocked(m.op)
	s.Settings.TouchRole(s.Authorization.State.CurrentUser, false)
	s.Authorization.State.CurrentUser = current
	s.Authorization.Revision++
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
	original := s.Authorization.State
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	for _, q := range []string{"SET ROLE a", "BEGIN", "SAVEPOINT p"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(q, err)
		}
	}
	mark := s.Authorization.State
	revision := s.Authorization.Revision
	ref := s.Transactions.Ref
	if err := m.end(nil); err != nil {
		t.Fatal(err)
	}
	if !s.Transactions.IdentityCaptured || s.Transactions.IdentityEntry != original || s.Transactions.RetainedMessage || s.Transactions.Ref != ref {
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
	if s.Authorization.State.CurrentUser != c.Ref || len(s.Transactions.Savepoints) != 1 {
		t.Fatal("release rewound identity")
	}
	if _, err := n.step(statement(t, "ROLLBACK TO p")); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.State != mark || s.Authorization.Revision <= revision || s.Transactions.IdentityEntry != original {
		t.Fatal("outer mark restoration")
	}
	if _, err := n.step(statement(t, "SAVEPOINT q")); err != nil {
		t.Fatal(err)
	}
	if !batchUnitIdentity(n, b.Ref) {
		t.Fatal("recovery mutation capacity")
	}
	revision = s.Authorization.Revision
	_, failure := n.step(statement(t, "SELECT missing"))
	if failure == nil {
		t.Fatal("admitted failure")
	}
	if s.TransactionState() != TxAborted || s.Authorization.State != mark || s.Authorization.Revision <= revision {
		t.Fatal("abort did not restore reached mark identity monotonically")
	}
	revision = s.Authorization.Revision
	if _, err := n.step(statement(t, "ROLLBACK TO q")); err != nil {
		t.Fatal(err)
	}
	if s.TransactionState() != TxActive || s.Authorization.State != mark || s.Authorization.Revision != revision {
		t.Fatal("same-state recovery invented mutation")
	}
	if _, err := n.step(statement(t, "ROLLBACK")); err != nil {
		t.Fatal(err)
	}
	if err := n.end(nil); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.State != original || s.Authorization.Revision <= revision || s.Transactions.IdentityCaptured {
		t.Fatal("outer rollback lost entry identity")
	}
}
func TestDriverBatchIdentityRevisionCapacity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE a")
	role, _ := e.Roles().Lookup("a")
	original := s.Authorization.State
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	s.Authorization.Revision = ^uint64(0) - 3
	if !batchUnitIdentity(m, role.Ref) || s.Authorization.Revision != ^uint64(0)-2 {
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
	if s.Authorization.State != original || s.Authorization.Revision != ^uint64(0)-1 {
		t.Fatal("rollback revision wrapped")
	}
	_, err := m.step(statement(t, "SET ROLE a"))
	var diagnostic interface{ SQLState() string }
	if err == nil || !strings.Contains(err.Error(), "session authorization revision exhausted") || errors.As(err, &diagnostic) || s.Authorization.State != original || s.Authorization.Revision != ^uint64(0)-1 {
		t.Fatal("exhaustion mutated or forged SQL diagnostic", err)
	}
	if end := m.end(err); end != err {
		t.Fatal(end)
	}
}
