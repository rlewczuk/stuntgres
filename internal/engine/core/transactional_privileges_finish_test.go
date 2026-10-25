package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
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
				var e *core.Engine
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
				peer, done := enginetest.RoleOperation(t, e, "postgres")
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
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				var general *session.CatalogAuthority
				if err == nil {
					general = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				oldRoles := e.RoleStore.Published
				if scope.Catalog == nil || scope.CatalogAuthority == nil || scope.CatalogAuthority.ACLRoles != oldRoles || op.Session.Transactions.Catalog != nil {
					t.Fatal("selected candidate escaped its statement or lost captured authority")
				}
				if roleMutation {
					roleAttributeCreate(t, e, peer, "finish_peer_role", false, parser.RoleOptions{})
				} else if _, err := e.Execute(peer, second, 0, nil); err != nil {
					t.Fatal("independent peer publication", err)
				}
				peerRoles := e.RoleStore.Published
				if peerRoles == oldRoles || peerRoles.Revision != oldRoles.Revision+1 || peerRoles.SameAuthority(oldRoles) == roleMutation {
					t.Fatal("peer did not establish the distinguishing authority change")
				}
				e.Mu.Lock()
				op.Session.Mu.Lock()
				err = core.EngineCatalogAuthorityPreflightLocked(e, general, nil)
				op.Session.Mu.Unlock()
				e.Mu.Unlock()
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) {
					t.Fatal("general statement revision fence was weakened", err)
				}
				err = scope.Finish(nil)
				if roleMutation {
					if !errors.As(err, &conflict) || e.RoleStore.Published != peerRoles || op.Session.Transactions.Catalog != nil {
						t.Fatal("actual peer role-root mutation was accepted", err)
					}
					return
				}
				if err != nil {
					t.Fatal("independent ACL publication invalidated selected Finish", err)
				}
				if e.RoleStore.Published != peerRoles || op.Session.Transactions.Catalog == nil {
					t.Fatal("Finish installed private ACLs early or lost the owner journal")
				}
				if defaults && e.Catalog.State.DefaultACL(firstKey) != nil {
					t.Fatal("private default policy published at Finish")
				}
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal("independent private ACL commit", err)
				}
				if e.RoleStore.Published.Revision != peerRoles.Revision+1 || !e.RoleStore.Published.SameAuthority(peerRoles) || e.Catalog.State.DefaultACL(peerKey) == nil {
					t.Fatal("combined commit lost peer policy or either revision delta")
				}
				if defaults {
					if record := e.Catalog.State.DefaultACL(firstKey); record == nil || authorization.ACLRights(record.ACL, firstKey.Creator, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, false) == 0 {
						t.Fatal("selected default policy effect lost")
					}
				} else {
					relation := e.Catalog.State.MustRelation(address)
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
				var e *core.Engine
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
				peer, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				ref, err := e.Begin(op, session.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer e.Rollback(op, ref)
				if _, err := e.Execute(op, initial, 0, nil); err != nil {
					t.Fatal("initial completed private ACL", err)
				}
				private, published := op.Session.Transactions.Catalog, e.RoleStore.Published
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
				if scope.Catalog == nil || scope.CatalogAuthority == nil || scope.CatalogAuthority.ACLRoles != private.Roles || scope.CatalogAuthority.ACLPublishedRoles != published || op.Session.Transactions.Catalog != private {
					t.Fatal("pending statement lost its original private/published authority pair")
				}
				delta := scope.Catalog.Roles.Revision - scope.Catalog.RolesBase.Revision
				if roleMutation {
					roleAttributeCreate(t, e, peer, "finish_private_peer_role", false, parser.RoleOptions{})
				} else if _, err := e.Execute(peer, second, 0, nil); err != nil {
					t.Fatal("independent peer ACL", err)
				}
				peerRoles, committed := e.RoleStore.Published, e.Catalog.State
				err = scope.Finish(nil)
				if private.Roles.Revision != privateRevision {
					t.Fatal("pending Finish mutated the original completed private record")
				}
				if roleMutation {
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) || e.RoleStore.Published != peerRoles || e.Catalog.State != committed {
						t.Fatal("private snapshot hid a pending actual-role authority change", err)
					}
					return
				}
				if err != nil || e.RoleStore.Published != peerRoles || e.Catalog.State != committed {
					t.Fatal("revision-only peer work rejected Finish or published early", err)
				}
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal("completed private grant-option commit", err)
				}
				if e.RoleStore.Published.Revision != peerRoles.Revision+delta || !e.RoleStore.Published.SameAuthority(peerRoles) {
					t.Fatal("completed private revision delta was lost")
				}
				if defaults {
					record := e.Catalog.State.DefaultACL(key)
					if record == nil || authorization.ACLRights(record.ACL, key.Creator, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, true) == 0 {
						t.Fatal("private default grant option was lost")
					}
				} else {
					relation := e.Catalog.State.MustRelation(address)
					if authorization.ACLRights(relation.Security.ColumnACL[3], relation.Owner, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, true) == 0 {
						t.Fatal("private column grant option was lost")
					}
				}
			})
		}
	}
}

func objectGrantUnitRequest() *parser.ObjectGrant {
	return &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select", Columns: []parser.Identifier{{Text: "b"}}}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "grant_unit"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}}
}
