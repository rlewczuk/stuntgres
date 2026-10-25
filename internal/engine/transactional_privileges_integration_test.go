package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Use the incoming actual-role fixtures and canonical selected execution, not a
// second role journal or a replay of either planner.
func TestTransactionalPrivilegesRoleIntegration(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		for _, mode := range []string{"acl-first", "role-first", "conflicting-principal", "frontier-ahead", "acl-only-peer-role", "acl-only-peer-role-refresh"} {
			name := map[bool]string{false: "object", true: "defaults"}[defaults] + "/" + mode
			t.Run(name, func(t *testing.T) {
				aclOnly := mode == "acl-only-peer-role" || mode == "acl-only-peer-role-refresh"
				var e *Engine
				var op *session.Operation
				var reader authorization.PrincipalRef
				var address ids.ObjectAddress
				var creator authorization.PrincipalRef
				if defaults {
					f := newDefaultUnitFixture(t)
					e, op, reader, creator = f.e, f.op, f.reader, f.owner
				} else {
					e, op, reader, address = objectGrantUnitFixture(t)
				}
				peer, done := roleOperation(t, e, "postgres")
				defer done()
				ref, err := e.Begin(op, session.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer e.Rollback(op, ref)
				create := func(actor *session.Operation, name string) authorization.RoleDescriptor {
					e.Mu.Lock()
					defer e.Mu.Unlock()
					next, d := roleTransactionalCandidate(t, e, e.OperationRolesLocked(actor), name)
					if err := InstallOperationRolesLocked(e, actor, next); err != nil {
						t.Fatal(err)
					}
					e.RoleStore.InstallPrincipalFrontierLocked(d.Ref.ID() + 1)
					return d
				}
				modify := func(actor *session.Operation, limit int32) {
					e.Mu.Lock()
					defer e.Mu.Unlock()
					record := e.OperationRolesLocked(actor)
					d, ok := record.Root.Read(authorization.PrincipalRef{}).Find(reader)
					if !ok {
						t.Fatal("missing fixture principal")
					}
					d.ConnectionLimit = limit
					d.AuthIDVersion = e.RoleStore.NextRevision()
					root, err := record.Root.PutDescriptor(d)
					if err != nil {
						t.Fatal(err)
					}
					if err := InstallOperationRolesLocked(e, actor, &access.RoleRecord{Root: root}); err != nil {
						t.Fatal(err)
					}
				}
				var local, other authorization.RoleDescriptor
				var pendingRef ids.TxRef
				if mode == "role-first" {
					local = create(op, "acl_mixed_local")
				}
				if mode == "conflicting-principal" {
					modify(op, 1)
				}
				if mode == "frontier-ahead" {
					pendingRef, err = e.Begin(peer, session.TxOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer e.Rollback(peer, pendingRef)
					other = create(peer, "acl_reserved_peer")
				}
				grantee := reader
				granteeName := "grant_reader"
				if defaults {
					granteeName = "default_reader"
				}
				if mode == "role-first" {
					grantee, granteeName = local.Ref, local.Name
				}
				var statement parser.Statement
				if defaults {
					statement = defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{granteeName})
				} else {
					request := objectGrantUnitRequest()
					request.Grantees = []parser.GrantGrantee{{Role: parser.RoleSpec{Name: granteeName}}}
					statement = request
				}
				frontier := e.RoleStore.NextRevision()
				if _, err := e.Execute(op, statement, 0, nil); err != nil {
					t.Fatal("selected ACL stage", err)
				}
				journal := op.Session.Transactions.Catalog
				if journal == nil || journal.Roles == nil {
					t.Fatal("missing private ACL pair")
				}
				delta := journal.Roles.Revision - journal.RolesBase.Revision
				dependency := false
				for resource, state := range e.Locks.Inspect(op.Session.Transactions.Ticket).Resources {
					if resource.Domain() == &e.Cluster.RoleDomain && resource.Address.ClassID == ids.RoleClassID && uint64(resource.Address.ObjectID) == grantee.ID() && !resource.RoleTuple && state.Holders[op.Session.Transactions.Ticket].Mode&lock.AccessShare != 0 {
						dependency = true
					}
				}
				if !dependency {
					t.Fatal("selected ACL omitted its exact shared principal dependency")
				}
				if mode == "role-first" && e.RoleStore.NextRevision() <= journal.Roles.Revision {
					t.Fatal("mixed ACL tail did not reserve its unique revision")
				}
				if mode == "acl-first" {
					local = create(op, "acl_mixed_local")
				}
				if mode == "conflicting-principal" {
					modify(peer, 2)
					before := e.RoleStore.Published
					_, err := e.Commit(op, ref)
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) || e.RoleStore.Published != before {
						t.Fatal("mixed conflicting principal update was accepted", err)
					}
					if defaults {
						if e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: creator, Class: authorization.DefaultTables}) != nil {
							t.Fatal("conflicting mixed policy leaked")
						}
					} else {
						relation := e.Catalog.State.MustRelation(address)
						if authorization.ACLRights(relation.Security.ColumnACL[3], relation.Owner, []authorization.PrincipalRef{grantee}, authorization.SelectPrivilege, false) != 0 {
							t.Fatal("conflicting mixed grant leaked")
						}
					}
					return
				}
				if mode != "frontier-ahead" {
					other = create(peer, "acl_mixed_peer")
				}
				peerRoles := e.RoleStore.Published
				expectedRevision := max(e.RoleStore.NextRevision(), peerRoles.Revision+delta)
				if mode == "acl-only-peer-role-refresh" {
					e.Mu.Lock()
					before := e.RoleStore.NextRevision()
					_, refreshErr := e.AuthorizationLocked(op)
					e.Mu.Unlock()
					current := op.Session.Transactions.Catalog
					if refreshErr != nil || current == journal || current.RolesBase != peerRoles || !current.Roles.SameAuthority(peerRoles) || current.Roles.Revision-current.RolesBase.Revision != delta || e.RoleStore.NextRevision() != before {
						t.Fatal("completed ACL refresh lost its delta or reserved a revision", refreshErr)
					}
					if journal.Roles.Revision-journal.RolesBase.Revision != delta || journal.Roles.SameAuthority(peerRoles) {
						t.Fatal("refresh mutated the captured old private authority")
					}
				}
				if _, err := e.Commit(op, ref); err != nil {
					t.Fatal("mixed/independent publication", err)
				}
				if aclOnly && (e.RoleStore.Published.Revision != expectedRevision || !e.RoleStore.Published.SameAuthority(peerRoles)) {
					t.Fatal("completed ACL publication lost the original delta or current authority")
				}
				if e.RoleStore.Published.Revision < frontier || e.RoleStore.NextRevision() <= e.RoleStore.Published.Revision {
					t.Fatal("publication reused or lowered canonical revision frontier")
				}
				if mode == "frontier-ahead" {
					if _, ok := e.Roles().Find(other.Ref); ok {
						t.Fatal("peer private role leaked through ACL publication")
					}
					if _, err := e.Commit(peer, pendingRef); err != nil {
						t.Fatal("incoming actual-role merge after ACL publication", err)
					}
				} else if !aclOnly {
					if _, ok := e.Roles().Find(local.Ref); !ok {
						t.Fatal("mixed publication lost its local principal")
					}
				}
				if _, ok := e.Roles().Find(other.Ref); !ok {
					t.Fatal("mixed publication lost the peer principal")
				}
				if defaults {
					record := e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: creator, Class: authorization.DefaultTables})
					if record == nil || authorization.ACLRights(record.ACL, creator, []authorization.PrincipalRef{grantee}, authorization.SelectPrivilege, false) == 0 {
						t.Fatal("mixed default policy lost")
					}
				} else {
					relation := e.Catalog.State.MustRelation(address)
					if authorization.ACLRights(relation.Security.ColumnACL[3], relation.Owner, []authorization.PrincipalRef{grantee}, authorization.SelectPrivilege, false) == 0 {
						t.Fatal("mixed object grant lost")
					}
				}
			})
		}
	}
}

func TestTransactionalPrivilegesAllocatorRefresh(t *testing.T) {
	f := newDefaultUnitFixture(t)
	ref, err := f.e.Begin(f.op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.e.Rollback(f.op, ref)
	f.grant(t, "tables", "select", nil)
	journal := f.op.Session.Transactions.Catalog
	peer, done := roleOperation(t, f.e, "postgres")
	defer done()
	if _, err := f.e.Execute(peer, defaultUnitStatement("sequences", "usage", []string{"default_owner"}, nil, []string{"default_reader"}), 0, nil); err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	before := f.e.RoleStore.NextRevision()
	err = EngineRefreshOperationRolesLocked(f.e, f.op)
	prepared, prepareErr := EnginePublicationRolesLocked(f.e, context.Background(), journal)
	unchanged := f.op.Session.Transactions.Catalog == journal && prepared == journal && f.e.RoleStore.NextRevision() == before
	f.e.Mu.Unlock()
	if err != nil || prepareErr != nil || !unchanged {
		t.Fatal("same-authority refresh/preparation changed logical delta or reserved a revision", err, prepareErr)
	}
	for _, frontier := range []uint64{f.e.RoleStore.Published.Revision + 10, ^uint64(0)} {
		f.e.Mu.Lock()
		f.e.RoleStore.InstallRevisionFrontierLocked(frontier)
		state, roles := f.e.Catalog.State, f.e.RoleStore.Published
		_, validationErr := EngineJournalRolePublicationLocked(f.e, journal, false)
		publication, materializeErr := EngineJournalRolePublicationLocked(f.e, journal, true)
		unpublished := f.e.Catalog.State == state && f.e.RoleStore.Published == roles && f.e.RoleStore.NextRevision() == frontier
		f.e.Mu.Unlock()
		if !unpublished {
			t.Fatal("validation/materialization installed roots or reserved a revision")
		}
		if frontier == ^uint64(0) {
			if validationErr == nil || materializeErr == nil {
				t.Fatal("exhausted canonical frontier accepted")
			}
		} else if validationErr != nil || materializeErr != nil || publication == nil || publication.Revision != frontier {
			t.Fatal("canonical frontier was ignored", validationErr, materializeErr)
		}
	}
}
