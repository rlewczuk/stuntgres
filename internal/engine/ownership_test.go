package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func ownershipUnitRoles() (e *Engine, owner, source, target, reader authorization.PrincipalRef) {
	e = New()
	owner = e.authorizationDomain.Principal(10)
	refs := []*authorization.PrincipalRef{&source, &target, &reader}
	names := []string{"ownership_source", "ownership_target", "ownership_reader"}
	publication := cloneRoleFixture(e.roles)
	for i, destination := range refs {
		e.nextPrincipal = uint64(100001 + i)
		ref, err := e.nextRolePrincipalLocked()
		if err != nil {
			panic(err)
		}
		*destination = ref
		e.nextPrincipal = ref.ID() + 1
		putFixtureRole(publication, authorization.RoleDescriptor{Ref: ref, Name: names[i], Inherit: true})
	}
	e.roles = publication
	return
}
func ownershipUnitExec(t *testing.T, e *Engine, q string) {
	t.Helper()
	if _, err := testExecute(e, context.Background(), statement(t, q), nil); err != nil {
		t.Fatal(q, err)
	}
}
func ownershipUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
func TestRoleOwnershipReciprocityAndStorage(t *testing.T) {
	e, owner, _, target, reader := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_t(id SERIAL PRIMARY KEY,payload INT)", "CREATE INDEX ownership_idx ON ownership_t(payload)", "INSERT INTO ownership_t(payload) VALUES(7)", "CREATE TYPE ownership_e AS ENUM('a')", "CREATE DOMAIN ownership_d AS INT", "CREATE TYPE ownership_c AS(id INT)"} {
		ownershipUnitExec(t, e, q)
	}
	base := e.CatalogRead()
	table := base.MustRelation(base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_t"}))
	// Explicit stable-sub-ID and associated-type ACL payloads are not public
	// GRANT-provider extensions. Install them in the canonical private stage.
	setup := e.BeginCatalogStage(base)
	copy := *table
	copy.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, reader, authorization.SelectPrivilege, 0)}}
	copy.ColumnACL = map[int32]authorization.ACLValue{table.Attributes[1]: {Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, reader, authorization.UpdatePrivilege, authorization.UpdatePrivilege)}}}
	copy.revision++
	if err := setup.Replace(&copy); err != nil {
		t.Fatal(err)
	}
	for _, id := range []sqltype.ID{table.RowType, base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)}).array} {
		d := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		next := *d
		next.acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, reader, authorization.UsagePrivilege, 0)}}
		next.aclSyntheticDefault = false
		next.revision++
		if err := setup.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	base = e.CatalogRead()
	oldTable := base.MustRelation(table.address)
	store := e.storage[table.address]
	rows := store.versions
	generators := make(map[ids.ObjectAddress]*sequenceGenerator)
	for a, g := range e.sequences {
		generators[a] = g
	}
	stage := e.BeginCatalogStage(base)
	for _, name := range []string{"ownership_t", "ownership_e", "ownership_d", "ownership_c"} {
		class := ids.TypeClassID
		if name == "ownership_t" {
			class = ids.RelationClassID
		}
		d := base.Get(base.NameAddress(ids.NameKey{ClassID: class, Namespace: table.namespace, Name: name}))
		if d == nil {
			t.Fatal("missing root", name)
		}
		if err := stageOwnership(stage, d, target); err != nil {
			t.Fatal(name, err)
		}
	}
	ownershipUnitPublish(t, e, stage)
	current := e.CatalogRead()
	compositeAddress := base.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: table.namespace, Name: "ownership_c"})
	composite := base.MustType(compositeAddress)
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(composite.array)}
	array := base.MustType(arrayAddress)
	backing := base.MustRelation(composite.owningRelation)
	movedComposite := current.MustType(compositeAddress)
	movedArray := current.MustType(arrayAddress)
	movedBacking := current.MustRelation(composite.owningRelation)
	if !array.typ.IsArray() || backing.RowType != composite.typ.ID() || backing.RowType == array.typ.ID() || movedComposite.owner != target || movedArray.owner != target || movedBacking.Owner != target || movedBacking.RowType != backing.RowType || movedComposite.array != composite.array || movedComposite.owningRelation != composite.owningRelation || movedArray.owningRelation != array.owningRelation || composite.owner != owner || array.owner != owner || backing.Owner != owner || movedComposite.typ != composite.typ || movedArray.typ != array.typ {
		t.Fatal("composite root/array/backing reciprocity or retained codec mutated")
	}
	for a, old := range base.Descriptors() {
		next := current.Get(a)
		if next == old {
			continue
		}
		got, _, _ := descriptorACL(next)
		if got != target {
			t.Fatal("associated owner", a, got)
		}
		if typ, ok := old.(*typeDescriptor); ok {
			if next.(*typeDescriptor).typ != typ.typ {
				t.Fatal("ownership replaced codec/signature", a)
			}
		}
	}
	moved := current.MustRelation(table.address)
	if moved.Owner != target || moved.Attributes[1] != oldTable.Attributes[1] || moved.ACL.Entries[0].Grantor != target || moved.ColumnACL[moved.Attributes[1]].Entries[0].Grantor != target {
		t.Fatal("table/column ACL or stable attribute", moved)
	}
	if e.storage[table.address] != store || len(store.versions) != len(rows) || &store.versions[0] != &rows[0] || oldTable.Owner != owner || oldTable.ACL.Entries[0].Grantor != owner {
		t.Fatal("retained data/descriptor changed")
	}
	for a, g := range generators {
		if e.sequences[a] != g {
			t.Fatal("ownership replaced sequence generator", a)
		}
	}
}
func TestRoleOwnershipBuiltinRootsAndRevisionHeadroom(t *testing.T) {
	e, _, _, target, _ := ownershipUnitRoles()
	base := e.CatalogRead()
	for _, d := range base.Descriptors() {
		switch d.Kind() {
		case ids.DatabaseObject:
			if !ownershipRoot(base, d) {
				t.Fatal("represented database missing from ownership roots", d)
			}
		case ids.NamespaceObject:
			expected := d.Address().ObjectID == ids.PublicNamespaceOID
			if ownershipRoot(base, d) != expected {
				t.Fatal("public exception or protected catalog namespace misclassified", d)
			}
		case ids.RelationObject, ids.TypeObject, ids.CollationObject:
			if ownershipRoot(base, d) {
				t.Fatal("immutable builtin executable ownership root", d)
			}
		}
	}
	ns := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 200000}, name: "ownership_overflow", revision: ids.DefinitionRevision(^uint64(0)), owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(ns); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	base = e.CatalogRead()
	stage = e.BeginCatalogStage(base)
	if err := stageOwnership(stage, ns, target); err == nil {
		t.Fatal("metadata overflow accepted")
	}
	if len(stage.descriptors) != 0 || e.catalog.state.Get(ns.address) != ns {
		t.Fatal("overflow changed private/live descriptor")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRoleOwnershipEffectFreeDescription(t *testing.T) {
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, stmt := range []parser.Statement{&parser.AlterOwner{Kind: "table", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "absent"}}}, Target: parser.RoleSpec{Name: "absent"}}, &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "absent"}}, Target: parser.RoleSpec{Name: "absent"}}, &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "absent"}}}} {
		before, roles := e.catalog.state, e.roles
		description, err := e.Describe(op, stmt)
		if err != nil || description.Parameters.Count != 0 || len(description.Parameters.Types) != 0 || len(description.Results) != 0 || e.catalog.state != before || e.roles != roles {
			t.Fatal("description resolves or mutates ownership", description, err)
		}
	}
}

func TestRoleOwnershipDirectProviderProtection(t *testing.T) {
	e, _, _, _, _ := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_guard_t(id INT)", "CREATE INDEX ownership_guard_idx ON ownership_guard_t(id)", "CREATE TYPE ownership_guard_c AS(id INT)"} {
		ownershipUnitExec(t, e, q)
	}
	for _, name := range []string{"pg_catalog.pg_class", "ownership_guard_idx", "ownership_guard_c"} {
		before, roles := e.catalog.state, e.roles
		if _, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE "+name+" OWNER TO ownership_target"), nil); err == nil {
			t.Fatal("direct provider/backing ownership accepted", name)
		}
		if e.catalog.state != before || e.roles != roles {
			t.Fatal("direct provider/backing mutation", name)
		}
	}
}
func TestRoleOwnershipInitialSourceCanonicalIdentity(t *testing.T) {
	e := New()
	graph := cloneRoleFixture(e.roles)
	initial := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	renamed := fixtureRole(graph, initial)
	renamed.Name = "renamed_initial"
	putFixtureRole(graph, renamed)

	ordinary := e.authorizationDomain.Principal(e.nextPrincipal)
	putFixtureRole(graph, authorization.RoleDescriptor{Ref: ordinary, Name: "postgres", Superuser: true, Inherit: true})

	auth := catalogAuthorization(e, e.CatalogRead())
	auth.roles = e.roleRead(auth.catalog, graph)
	auth.roleRecord = graph
	retained := e.roles
	for _, reassign := range []bool{false, true} {
		refs, protected, err := ownedSources(auth, []parser.RoleSpec{{Name: "postgres"}, {Name: "renamed_initial"}, {Name: "renamed_initial"}}, reassign)
		if err != nil || len(refs) != 2 || !refs[ordinary] || !refs[initial] || protected != initial {
			t.Fatal("canonical initial source or deduplicated union", refs, protected, err)
		}
		refs, protected, err = ownedSources(auth, []parser.RoleSpec{{Name: "postgres"}}, reassign)
		if err != nil || len(refs) != 1 || protected != (authorization.PrincipalRef{}) {
			t.Fatal("ordinary familiar-name superuser was protected", refs, protected, err)
		}
		if _, _, err = ownedSources(auth, []parser.RoleSpec{{Name: "renamed_initial"}, {Name: "missing_source"}}, reassign); err == nil {
			t.Fatal("initial protection masked late operand resolution")
		}
	}
	if fixtureRole(graph, initial).Predefined() || e.roles != retained || fixtureRole(e.roles, initial).Name != "postgres" {
		t.Fatal("protection changed unrelated predefined flag or live initial identity")
	}
}
