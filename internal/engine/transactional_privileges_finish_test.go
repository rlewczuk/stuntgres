package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Hold a real selected statement after Execute staged its candidate, publish an
// independent peer change, then Finish. This schedule is deterministic and does
// not depend on winning a goroutine race in the public concurrent-writer case.
func TestTransactionalPrivilegesFinishAuthority(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		for _, roleMutation := range []bool{false, true} {
			name := map[bool]string{false: "object", true: "defaults"}[defaults] + "/" + map[bool]string{false: "peer-acl", true: "peer-role"}[roleMutation]
			t.Run(name, func(t *testing.T) {
				var e *Engine
				var op *session.Operation
				var reader authorization.PrincipalRef
				var address ids.ObjectAddress
				var first parser.Statement
				var firstKey, peerKey catalog.DefaultACLKey
				var second *parser.AlterDefaultPrivileges
				if defaults {
					f := newDefaultUnitFixture(t)
					e, op, reader = f.e, f.op, f.reader
					first = defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
					second = defaultUnitStatement("sequences", "usage", []string{"default_owner"}, nil, []string{"default_reader"})
					firstKey = catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
					peerKey = catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultSequences}
				} else {
					e, op, reader, address = objectGrantUnitFixture(t)
					first = objectGrantUnitRequest()
					second = defaultUnitStatement("tables", "select", nil, nil, []string{"grant_reader"})
					peerKey = catalog.DefaultACLKey{Creator: op.RoleState().CurrentUser, Class: authorization.DefaultTables}
				}
				peer, done := roleOperation(t, e, "postgres")
				defer done()
				ref, err := e.Begin(op, session.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = scope.Finish(context.Canceled) }()
				if _, err := scope.Execute(first, 0, nil); err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				auth, err := e.authorizationLocked(op)
				var general *session.CatalogAuthority
				if err == nil {
					general = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
				}
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				oldRoles := e.roles.Published
				if scope.catalog == nil || scope.catalogAuthority == nil || scope.catalogAuthority.ACLRoles != oldRoles || op.Session.Transactions.Catalog != nil {
					t.Fatal("selected candidate escaped its statement or lost captured authority")
				}
				if roleMutation {
					roleAttributeCreate(t, e, peer, "finish_peer_role", false, parser.RoleOptions{})
				} else if _, err := e.Execute(peer, second, 0, nil); err != nil {
					t.Fatal("independent peer publication", err)
				}
				peerRoles := e.roles.Published
				if peerRoles == oldRoles || peerRoles.Revision != oldRoles.Revision+1 || peerRoles.SameAuthority(oldRoles) == roleMutation {
					t.Fatal("peer did not establish the distinguishing authority change")
				}
				e.mu.Lock()
				op.Session.Mu.Lock()
				err = e.catalogAuthorityPreflightLocked(general, nil)
				op.Session.Mu.Unlock()
				e.mu.Unlock()
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) {
					t.Fatal("general statement revision fence was weakened", err)
				}
				err = scope.Finish(nil)
				if roleMutation {
					if !errors.As(err, &conflict) || e.roles.Published != peerRoles || op.Session.Transactions.Catalog != nil {
						t.Fatal("actual peer role-root mutation was accepted", err)
					}
					return
				}
				if err != nil {
					t.Fatal("independent ACL publication invalidated selected Finish", err)
				}
				if e.roles.Published != peerRoles || op.Session.Transactions.Catalog == nil {
					t.Fatal("Finish installed private ACLs early or lost the owner journal")
				}
				if defaults && e.catalog.State.DefaultACL(firstKey) != nil {
					t.Fatal("private default policy published at Finish")
				}
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal("independent private ACL commit", err)
				}
				if e.roles.Published.Revision != peerRoles.Revision+1 || !e.roles.Published.SameAuthority(peerRoles) || e.catalog.State.DefaultACL(peerKey) == nil {
					t.Fatal("combined commit lost peer policy or either revision delta")
				}
				if defaults {
					if record := e.catalog.State.DefaultACL(firstKey); record == nil || authorization.ACLRights(record.ACL, firstKey.Creator, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, false) == 0 {
						t.Fatal("selected default policy effect lost")
					}
				} else {
					relation := e.catalog.State.MustRelation(address)
					if authorization.ACLRights(relation.Security.ColumnACL[3], relation.Owner, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, false) == 0 {
						t.Fatal("selected column grant effect lost")
					}
				}
			})
		}
	}
}

// A prior completed ACL leaves a private role record in the transaction. That
// record must not hide an actual committed authority change while a subsequent
// selected statement is still pending between Execute and Finish.
func TestTransactionalPrivilegesPendingPrivateAuthority(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		for _, roleMutation := range []bool{false, true} {
			name := map[bool]string{false: "object", true: "defaults"}[defaults] + "/" + map[bool]string{false: "peer-acl", true: "peer-role"}[roleMutation]
			t.Run(name, func(t *testing.T) {
				var e *Engine
				var op *session.Operation
				var reader authorization.PrincipalRef
				var address ids.ObjectAddress
				var key catalog.DefaultACLKey
				var initial, pending parser.Statement
				var second *parser.AlterDefaultPrivileges
				if defaults {
					f := newDefaultUnitFixture(t)
					e, op, reader = f.e, f.op, f.reader
					initial = defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
					next := defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
					next.GrantOption = true
					pending = next
					key = catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
					second = defaultUnitStatement("sequences", "usage", []string{"default_owner"}, nil, []string{"default_reader"})
				} else {
					e, op, reader, address = objectGrantUnitFixture(t)
					initial = objectGrantUnitRequest()
					next := objectGrantUnitRequest()
					next.GrantOption = true
					pending = next
					second = defaultUnitStatement("tables", "select", nil, nil, []string{"grant_reader"})
				}
				peer, done := roleOperation(t, e, "postgres")
				defer done()
				ref, err := e.Begin(op, session.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer e.Rollback(op, ref)
				if _, err := e.Execute(op, initial, 0, nil); err != nil {
					t.Fatal("initial completed private ACL", err)
				}
				private, published := op.Session.Transactions.Catalog, e.roles.Published
				if private == nil || private.Roles == nil {
					t.Fatal("initial ACL has no private authority")
				}
				privateRevision := private.Roles.Revision
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = scope.Finish(context.Canceled) }()
				if _, err := scope.Execute(pending, 0, nil); err != nil {
					t.Fatal("pending private grant option", err)
				}
				if scope.catalog == nil || scope.catalogAuthority == nil || scope.catalogAuthority.ACLRoles != private.Roles || scope.catalogAuthority.ACLPublishedRoles != published || op.Session.Transactions.Catalog != private {
					t.Fatal("pending statement lost its original private/published authority pair")
				}
				delta := scope.catalog.Roles.Revision - scope.catalog.RolesBase.Revision
				if roleMutation {
					roleAttributeCreate(t, e, peer, "finish_private_peer_role", false, parser.RoleOptions{})
				} else if _, err := e.Execute(peer, second, 0, nil); err != nil {
					t.Fatal("independent peer ACL", err)
				}
				peerRoles, committed := e.roles.Published, e.catalog.State
				err = scope.Finish(nil)
				if private.Roles.Revision != privateRevision {
					t.Fatal("pending Finish mutated the original completed private record")
				}
				if roleMutation {
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) || e.roles.Published != peerRoles || e.catalog.State != committed {
						t.Fatal("private snapshot hid a pending actual-role authority change", err)
					}
					return
				}
				if err != nil || e.roles.Published != peerRoles || e.catalog.State != committed {
					t.Fatal("revision-only peer work rejected Finish or published early", err)
				}
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal("completed private grant-option commit", err)
				}
				if e.roles.Published.Revision != peerRoles.Revision+delta || !e.roles.Published.SameAuthority(peerRoles) {
					t.Fatal("completed private revision delta was lost")
				}
				if defaults {
					record := e.catalog.State.DefaultACL(key)
					if record == nil || authorization.ACLRights(record.ACL, key.Creator, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, true) == 0 {
						t.Fatal("private default grant option was lost")
					}
				} else {
					relation := e.catalog.State.MustRelation(address)
					if authorization.ACLRights(relation.Security.ColumnACL[3], relation.Owner, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, true) == 0 {
						t.Fatal("private column grant option was lost")
					}
				}
			})
		}
	}
}
