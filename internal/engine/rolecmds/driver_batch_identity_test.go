package rolecmds_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Exercise the same typed role/identity journals without adding SQL capability.
func batchUnitIdentity(m *batchUnitMessage, current authorization.RoleDescriptor, active bool) bool {
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
	rolecmds.TouchOperationIdentityLocked(m.op)
	s.Settings.TouchRole(s.Authorization.State, false)
	s.Authorization.State.CurrentUser = current.Ref
	s.Authorization.State.ActiveRole, s.Authorization.State.RoleText = active, ""
	if active {
		s.Authorization.State.RoleText = current.Name
	}
	s.Authorization.Revision++
	return true
}
func TestDriverBatchIdentityPromotionMarksAndRevision(t *testing.T) {
	t.Run("self-none", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		self, _ := e.Roles().Lookup("postgres")
		entry := s.Authorization.State
		m := batchUnitStart(t, s, context.Background())
		defer m.end(context.Canceled)
		if _, err := m.step(enginetest.Statement(t, "BEGIN")); err != nil {
			t.Fatal(err)
		}
		if !batchUnitIdentity(m, self, true) {
			t.Fatal("self selection rejected")
		}
		selected := s.Authorization.State
		if selected.CurrentUser != entry.CurrentUser || !selected.ActiveRole || selected.RoleText != self.Name {
			t.Fatal("self selection collapsed", selected)
		}
		name := parser.Identifier{Text: "self_mark"}
		if _, err := m.step(&parser.SavepointControl{Kind: parser.CreateSavepoint, Name: name}); err != nil {
			t.Fatal(err)
		}
		if !batchUnitIdentity(m, self, false) || s.Authorization.State != entry {
			t.Fatal("NONE did not clear the complete role triple")
		}
		revision := s.Authorization.Revision
		if _, err := m.step(&parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: name}); err != nil {
			t.Fatal(err)
		}
		if s.Authorization.State != selected || s.Authorization.Revision != revision+1 {
			t.Fatal("active-only mark restoration lost state or revision")
		}
		if _, err := m.step(enginetest.Statement(t, "ROLLBACK")); err != nil {
			t.Fatal(err)
		}
		if err := m.end(nil); err != nil || s.Authorization.State != entry || s.Authorization.Revision <= revision+1 {
			t.Fatal("active-only outer rollback lost state or revision", err)
		}
	})
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE ROLE a", "CREATE ROLE b", "CREATE ROLE c"} {
		enginetest.SessionSQL(t, s, q)
	}
	b, _ := e.Roles().Lookup("b")
	c, _ := e.Roles().Lookup("c")
	original := s.Authorization.State
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	for _, q := range []string{"SET ROLE a", "BEGIN", "SAVEPOINT p"} {
		if _, err := m.step(enginetest.Statement(t, q)); err != nil {
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
	if !batchUnitIdentity(n, b, true) {
		t.Fatal("internal mutation capacity")
	}
	if _, err := n.step(enginetest.Statement(t, "SAVEPOINT q")); err != nil {
		t.Fatal(err)
	}
	if !batchUnitIdentity(n, c, true) {
		t.Fatal("nested mutation capacity")
	}
	if _, err := n.step(enginetest.Statement(t, "RELEASE q")); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.State.CurrentUser != c.Ref || len(s.Transactions.Savepoints) != 1 {
		t.Fatal("release rewound identity")
	}
	if _, err := n.step(enginetest.Statement(t, "ROLLBACK TO p")); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.State != mark || s.Authorization.Revision <= revision || s.Transactions.IdentityEntry != original {
		t.Fatal("outer mark restoration")
	}
	if _, err := n.step(enginetest.Statement(t, "SAVEPOINT q")); err != nil {
		t.Fatal(err)
	}
	if !batchUnitIdentity(n, b, true) {
		t.Fatal("recovery mutation capacity")
	}
	revision = s.Authorization.Revision
	_, failure := n.step(enginetest.Statement(t, "SELECT missing"))
	if failure == nil {
		t.Fatal("admitted failure")
	}
	if s.TransactionState() != session.TxAborted || s.Authorization.State != mark || s.Authorization.Revision <= revision {
		t.Fatal("abort did not restore reached mark identity monotonically")
	}
	revision = s.Authorization.Revision
	if _, err := n.step(enginetest.Statement(t, "ROLLBACK TO q")); err != nil {
		t.Fatal(err)
	}
	if s.TransactionState() != session.TxActive || s.Authorization.State != mark || s.Authorization.Revision != revision {
		t.Fatal("same-state recovery invented mutation")
	}
	if _, err := n.step(enginetest.Statement(t, "ROLLBACK")); err != nil {
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
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE a")
	self, _ := e.Roles().Lookup("postgres")
	original := s.Authorization.State
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	s.Authorization.Revision = ^uint64(0) - 3
	if !batchUnitIdentity(m, self, true) || s.Authorization.Revision != ^uint64(0)-2 {
		t.Fatal("reserved mutation denied")
	}
	if batchUnitIdentity(m, self, false) {
		t.Fatal("rollback reservation consumed")
	}
	if _, err := m.step(enginetest.Statement(t, "BEGIN")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.step(enginetest.Statement(t, "ROLLBACK")); err != nil {
		t.Fatal(err)
	}
	if s.Authorization.State != original || s.Authorization.Revision != ^uint64(0)-1 {
		t.Fatal("rollback revision wrapped")
	}
	_, err := m.step(enginetest.Statement(t, "SET ROLE a"))
	var diagnostic interface{ SQLState() string }
	if err == nil || !strings.Contains(err.Error(), "session authorization revision exhausted") || errors.As(err, &diagnostic) || s.Authorization.State != original || s.Authorization.Revision != ^uint64(0)-1 {
		t.Fatal("exhaustion mutated or forged SQL diagnostic", err)
	}
	if end := m.end(err); end != err {
		t.Fatal(end)
	}
}
