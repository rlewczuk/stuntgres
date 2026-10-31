package roles_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Use the incoming actual-role fixtures and canonical selected execution, not a
// second role journal or a replay of either planner.
func TestTransactionalPrivilegesRoleIntegration(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		for _, mode := range []string{"acl-first", "role-first", "conflicting-principal", "frontier-ahead", "acl-only-peer-role", "acl-only-peer-role-refresh"} {
			name := map[bool]string{false: "object", true: "defaults"}[defaults] + "/" + mode
			t.Run(name, func(t *testing.T) {
				aclOnly := mode == "acl-only-peer-role" || mode == "acl-only-peer-role-refresh"
				var e *core.Engine
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
				peer, done := enginetest.RoleOperation(t, e, "postgres")
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
					if err := core.InstallOperationRolesLocked(e, actor, next); err != nil {
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
					if err := core.InstallOperationRolesLocked(e, actor, &access.RoleRecord{Root: root}); err != nil {
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
					statement = enginetest.DefaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{granteeName})
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

// These units construct immutable candidates and exercise the owning package
// publication/restoration functions, not the parser or public SQL adapter.
func roleTransactionalCandidate(t *testing.T, e *core.Engine, base *access.RoleRecord, name string) (*access.RoleRecord, authorization.RoleDescriptor) {
	t.Helper()
	ref, err := e.RoleStore.PrincipalCandidateLocked()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		t.Fatal(err)
	}
	d := authorization.RoleDescriptor{Ref: ref, Name: name, Inherit: true, ConnectionLimit: -1, AuthIDVersion: revision}
	root, err := base.Root.PutDescriptor(d)
	if err != nil {
		t.Fatal(err)
	}
	return &access.RoleRecord{Root: root}, d
}

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	t.Helper()
	e := engine.New()
	roles := enginetest.CloneRoleFixture(e.RoleStore.Published)
	refs := make([]authorization.PrincipalRef, 3)
	for i, name := range []string{"default_owner", "default_reader", "default_member"} {
		ref := e.RoleStore.Domain.Principal(uint64(20001 + i))
		refs[i] = ref
		enginetest.PutFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})

	}
	enginetest.SetFixtureEdges(roles, append(append([]authorization.Membership(nil), enginetest.FixtureEdges(roles)...), authorization.Membership{Role: refs[0], Member: refs[2], Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Inherit: true, Set: true}))
	// Install a fully identified graph before transactions branch from this
	// fixture. Candidate-only helpers intentionally leave new edge IDs at zero.
	root, frontier, err := roles.Root.AssignMembershipOIDs(authorization.FirstMembershipOID)
	if err != nil {
		t.Fatal(err)
	}
	roles.Root = root
	e.RoleStore.InstallMembershipFrontierLocked(frontier)
	e.RoleStore.PublishLocked(roles)
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20010}
	next := catalog.CloneState(e.Catalog.State)
	namespace := &catalog.Namespace{ObjectAddress: schema, ObjectName: "default_schema", ObjectRevision: 2, Owner: refs[0]}
	next.SetDescriptor(schema, namespace)
	next.BindName(catalog.DescriptorNameKey(namespace), schema)
	e.Catalog.PublishLocked(next)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

// These fixtures exercise the private catalog planner directly, not public SQL.
func objectGrantUnitFixture(t *testing.T) (*core.Engine, *session.Operation, authorization.PrincipalRef, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	reader := roleAttributeCreate(t, e, op, "grant_reader", true, parser.RoleOptions{}).Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900001}
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	typ, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4 fixture type")
	}
	relation := &catalog.Relation{ObjectAddress: address, ObjectNamespace: namespace, ObjectName: "grant_unit", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: op.RoleState().CurrentUser, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}}, Index: map[string]int{"a": 0, "b": 1}, Attributes: []int32{1, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}}
	relation.TableMethod = 2
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetDescriptor(address, relation)
	e.Catalog.State.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: relation.Name()}, address)
	return e, op, reader, address
}

func objectGrantUnitRequest() *parser.ObjectGrant {
	return &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select", Columns: []parser.Identifier{{Text: "b"}}}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "grant_unit"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}}
}

type defaultUnitFixture struct {
	e                     *core.Engine
	op                    *session.Operation
	owner, reader, member authorization.PrincipalRef
	schema                ids.ObjectAddress
}

func (f defaultUnitFixture) apply(s *parser.AlterDefaultPrivileges) error {
	_, err := f.e.Execute(f.op, s, 0, nil)
	return err
}

func (f defaultUnitFixture) grant(t *testing.T, class, privilege string, schemas []string) *parser.AlterDefaultPrivileges {
	t.Helper()
	s := enginetest.DefaultUnitStatement(class, privilege, []string{"default_owner"}, schemas, []string{"default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f defaultUnitFixture) actor(ref authorization.PrincipalRef) {
	f.e.Mu.Lock()
	f.op.Session.Mu.Lock()
	f.op.Session.Authorization.State.CurrentUser = ref
	f.op.Session.Authorization.Revision++
	f.op.Session.Mu.Unlock()
	f.e.Mu.Unlock()
}

func roleAttributeCreate(t *testing.T, e *core.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func roleAttributeApply(t *testing.T, e *core.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}
