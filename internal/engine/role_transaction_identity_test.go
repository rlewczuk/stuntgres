package engine

import (
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func roleTransactionalAST(t *testing.T, e *Engine, s *session.Session, statement parser.Statement) {
	t.Helper()
	op := transactionOperation(t, s)
	_, err := e.Execute(op, statement, 0, nil)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRoleTransactionalIdentityRestoration(t *testing.T) {
	for _, commit := range []bool{false, true} {
		label := "rollback"
		if commit {
			label = "commit"
		}
		t.Run(label, func(t *testing.T) {
			e := New()
			s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, name := range []string{"identity_unit_a", "identity_unit_b"} {
				roleTransactionalAST(t, e, s, &parser.CreateRole{Name: parser.Identifier{Text: name}})
			}
			entry, peerEntry := s.Authorization.State, peer.Authorization.State
			ref := transactionBegin(t, s)
			roleTransactionalAST(t, e, s, &parser.RoleSelection{Name: "identity_unit_a", Local: true})
			roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Name: "identity_unit_b"})
			selected := s.Authorization.State
			if selected.Authenticated != entry.Authenticated || selected.ResetRole != entry.ResetRole || selected.SessionUser != selected.CurrentUser || selected.SessionUser == entry.SessionUser || s.Transactions.IdentityEntry != entry {
				t.Fatal("session selection lost physical entry identity")
			}
			name := parser.Identifier{Text: "identity_mark"}
			roleTransactionalAST(t, e, s, &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: name})
			mark := s.Transactions.Savepoints[len(s.Transactions.Savepoints)-1]
			for range 2 {
				roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Name: "identity_unit_a"})
				roleTransactionalAST(t, e, s, &parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: name})
				if s.Authorization.State != selected || s.Settings.RoleAction != mark.Settings.RoleAction {
					t.Fatal("inline restore lost identity or typed role action")
				}
			}
			roleTransactionalAST(t, e, s, &parser.SavepointControl{Kind: parser.ReleaseSavepoint, Name: name})
			roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Name: "identity_unit_a"})
			// The failed-statement restoration path consumes a cloned settings mark
			// and exact refs; it neither resolves names nor repeats SET eligibility.
			restoreSavepoint(s.Transactions, mark, mark.Settings.Clone())
			e.locks.Restore(s.Transactions.Ticket, mark.Locks)
			if s.Authorization.State != selected || s.Settings.RoleAction != mark.Settings.RoleAction || peer.Authorization.State != peerEntry {
				t.Fatal("cached restoration changed peer/physical identity or role action")
			}
			revision := s.Authorization.Revision
			s.Authorization.Revision = ^uint64(0) - 1
			op := transactionOperation(t, s)
			e.mu.Lock()
			err := e.switchSessionRoleLocked(op, entry.CurrentUser, false, true)
			e.mu.Unlock()
			op.Done()
			if err == nil || s.Authorization.State != selected || s.Settings.RoleAction != mark.Settings.RoleAction {
				t.Fatal("exhausted revision changed identity/settings")
			}
			s.Authorization.Revision = revision
			transactionEnd(t, s, ref, commit)
			want := entry
			if commit {
				want = selected
			}
			if s.Authorization.State != want || peer.Authorization.State != peerEntry || s.Transactions.IdentityCaptured || s.Settings.RoleAction.Present {
				t.Fatal("outer finish restored the wrong complete identity")
			}
		})
	}
}

func TestRoleTransactionalProtocolIdentityStatus(t *testing.T) {
	e := New()
	s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	check := func(name string, superuser, retired bool) {
		t.Helper()
		identity, revision := s.Authorization.State, s.Authorization.Revision
		journal, published := s.Transactions.Catalog, e.roles.published
		principal, version := e.roles.nextPrincipal, e.roles.nextRevision
		got, privileged, missing, err := e.ProtocolAuthorizationStatus(s)
		if err != nil || missing != retired || !retired && (got != name || privileged != superuser) {
			t.Fatal("identity status", got, privileged, missing, err)
		}
		if s.Authorization.State != identity || s.Authorization.Revision != revision || s.Transactions.Catalog != journal || e.roles.published != published || e.roles.nextPrincipal != principal || e.roles.nextRevision != version {
			t.Fatal("transport snapshot changed identity, roots or allocation")
		}
	}
	check("postgres", true, false)
	ref := transactionBegin(t, s)
	mark := parser.Identifier{Text: "status_mark"}
	roleTransactionalAST(t, e, s, &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: mark})
	roleTransactionalAST(t, e, s, &parser.CreateRole{Name: parser.Identifier{Text: "status_target"}})
	roleTransactionalAST(t, e, s, &parser.RoleSelection{Name: "status_target"})
	check("postgres", false, false)
	roleTransactionalAST(t, e, s, &parser.RoleSelection{Reset: true})
	check("postgres", true, false)
	roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Name: "status_target"})
	first := s.Authorization.State.SessionUser
	check("status_target", false, false)
	if name, superuser, retired, err := e.ProtocolAuthorizationStatus(peer); err != nil || retired || name != "postgres" || !superuser {
		t.Fatal("private identity leaked to peer", name, superuser, retired, err)
	}
	roleTransactionalAST(t, e, s, &parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: mark})
	check("postgres", true, false)
	roleTransactionalAST(t, e, s, &parser.CreateRole{Name: parser.Identifier{Text: "status_target"}})
	roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Name: "status_target"})
	if s.Authorization.State.SessionUser == first {
		t.Fatal("savepoint rollback reused the private principal")
	}
	roleTransactionalAST(t, e, s, &parser.SavepointControl{Kind: parser.ReleaseSavepoint, Name: mark})
	transactionEnd(t, s, ref, true)
	selected := s.Authorization.State
	check("status_target", false, false)
	ref = transactionBegin(t, s)
	roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Default: true})
	check("postgres", true, false)
	transactionEnd(t, s, ref, false)
	if s.Authorization.State != selected {
		t.Fatal("outer rollback did not restore the cached identity")
	}
	check("status_target", false, false)

	// Retirement is a known cached identity outcome, not permission to execute
	// as a same-name replacement and not a new error merely for reporting status.
	roleTransactionalAST(t, e, peer, &parser.DropRole{Names: []string{"status_target"}})
	check("", false, true)
	roleTransactionalAST(t, e, peer, &parser.CreateRole{Name: parser.Identifier{Text: "status_target"}})
	replacement, exists := e.Roles().Lookup("status_target")
	if !exists || replacement.Ref == selected.SessionUser || s.Authorization.State != selected {
		t.Fatal("same-name recreation adopted a cached principal")
	}
	check("", false, true)
	roleTransactionalAST(t, e, s, &parser.SessionAuthorization{Default: true})
	check("postgres", true, false)

	foreign := New()
	if _, _, retired, err := foreign.ProtocolAuthorizationStatus(s); err == nil || retired {
		t.Fatal("foreign attachment was treated as retired", retired, err)
	}
	alien, _ := foreign.Roles().Lookup("postgres")
	s.Mu.Lock()
	valid := s.Authorization.State
	s.Authorization.State.SessionUser = alien.Ref
	s.Mu.Unlock()
	_, _, retired, err := e.ProtocolAuthorizationStatus(s)
	s.Mu.Lock()
	s.Authorization.State = valid
	s.Mu.Unlock()
	if err == nil || retired {
		t.Fatal("foreign principal was treated as retired", retired, err)
	}
	check("postgres", true, false)
}
