package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type defaultUnitFixture struct {
	e                     *Engine
	op                    *Operation
	owner, reader, member authorization.PrincipalRef
	schema                ids.ObjectAddress
}

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	t.Helper()
	e := New()
	roles := cloneRoleFixture(e.roles.published)
	refs := make([]authorization.PrincipalRef, 3)
	for i, name := range []string{"default_owner", "default_reader", "default_member"} {
		ref := e.roles.domain.Principal(uint64(20001 + i))
		refs[i] = ref
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})

	}
	setFixtureEdges(roles, append(append([]authorization.Membership(nil), fixtureEdges(roles)...), authorization.Membership{Role: refs[0], Member: refs[2], Grantor: e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Inherit: true, Set: true}))
	e.roles.published = roles
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20010}
	next := catalog.CloneState(e.catalog.state)
	namespace := &catalog.Namespace{ObjectAddress: schema, ObjectName: "default_schema", ObjectRevision: 2, Owner: refs[0]}
	next.SetDescriptor(schema, namespace)
	next.BindName(catalog.DescriptorNameKey(namespace), schema)
	e.catalog.state = next
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

func defaultUnitStatement(class, privilege string, creators, schemas, grantees []string) *parser.AlterDefaultPrivileges {
	s := &parser.AlterDefaultPrivileges{Class: class, Privileges: []parser.GrantPrivilege{{Name: privilege, All: privilege == "all"}}}
	if creators != nil {
		option := parser.DefaultPrivilegesOption{Roles: make([]parser.RoleSpec, len(creators))}
		for i, name := range creators {
			option.Roles[i] = parser.RoleSpec{Name: name}
		}
		s.Options = append(s.Options, option)
	}
	if schemas != nil {
		option := parser.DefaultPrivilegesOption{Schemas: make([]parser.Identifier, len(schemas))}
		for i, name := range schemas {
			option.Schemas[i] = parser.Identifier{Text: name}
		}
		s.Options = append(s.Options, option)
	}
	for _, name := range grantees {
		s.Grantees = append(s.Grantees, parser.GrantGrantee{Public: name == "PUBLIC", Role: parser.RoleSpec{Name: name}})
	}
	return s
}
func (f defaultUnitFixture) apply(s *parser.AlterDefaultPrivileges) error {
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	auth, err := f.e.authorizationLocked(f.op)
	if err != nil {
		return err
	}
	_, err = f.e.alterDefaultPrivilegesLocked(f.op, auth, s)
	return err
}
func (f defaultUnitFixture) grant(t *testing.T, class, privilege string, schemas []string) *parser.AlterDefaultPrivileges {
	t.Helper()
	s := defaultUnitStatement(class, privilege, []string{"default_owner"}, schemas, []string{"default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	return s
}
func (f defaultUnitFixture) actor(ref authorization.PrincipalRef) {
	f.e.mu.Lock()
	f.op.session.mu.Lock()
	f.op.session.authorization.state.CurrentUser = ref
	f.op.session.authorization.revision++
	f.op.session.mu.Unlock()
	f.e.mu.Unlock()
}
func defaultUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" {
		t.Fatalf("diagnostic = %#v; want %s %q", err, code, message)
	}
}
func defaultUnitReaderEntry(f defaultUnitFixture, privileges, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: privileges, GrantOptions: options}
}

// defaultUnitTable owns Engine.mu; callers must not hold it. Resolve the
// immutable stage-base carrier first, without a locking Engine.CatalogRead.
func defaultUnitTable(t *testing.T, f defaultUnitFixture, stage *CatalogStage, name string, owner authorization.PrincipalRef) *catalog.Relation {
	t.Helper()
	typ, ok := stage.Base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("stage has no int4 carrier")
	}
	f.e.mu.Lock()
	d, err := f.e.newTableDescriptorLocked(f.schema, name, owner, []catalog.Column{{Name: "x", Usage: typ.Usage()}}, nil)
	if err == nil {
		d.Security.ACL = stage.creationACL(owner, f.schema, authorization.DefaultTables)
		err = f.e.stageRowTypeLocked(stage, d)
	}
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Create(d); err != nil {
		t.Fatal(err)
	}
	return d
}
func defaultUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) error {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalog(p, PublicationToken{Epoch: stage.Base.Epoch()})
	return err
}
