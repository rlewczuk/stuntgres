package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func ownershipUnitRoles() (e *Engine, owner, source, target, reader PrincipalRef) {
	owner, source, target, reader = grantUnitRoles()
	e = owner.owner
	for name, ref := range map[string]PrincipalRef{"ownership_source": source, "ownership_target": target, "ownership_reader": reader} {
		role := e.roles.roles[ref]
		role.Name = name
		e.roles.roles[ref] = role
		e.roles.names[name] = ref
	}
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
func TestRoleOwnershipACLScopes(t *testing.T) {
	_, old, next, third, reader := ownershipUnitRoles()
	for _, mask := range []Privileges{SelectPrivilege | UpdatePrivilege, UsagePrivilege | CreatePrivilege} {
		original := ACLValue{Explicit: true, Entries: []ACLEntry{
			{Grantor: old, Grantee: ACLGrantee{Role: old}, Privileges: mask, GrantOptions: mask},
			{Grantor: next, Grantee: ACLGrantee{Role: next}, Privileges: SelectPrivilege},
			{Grantor: old, Grantee: ACLGrantee{Role: reader}, Privileges: mask},
			{Grantor: third, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege},
		}}
		retained := append([]ACLEntry(nil), original.Entries...)
		got := schemaACLOwner(original, old, next)
		want := ACLValue{Explicit: true, Entries: []ACLEntry{
			{Grantor: next, Grantee: ACLGrantee{Role: next}, Privileges: mask | SelectPrivilege, GrantOptions: mask},
			{Grantor: next, Grantee: ACLGrantee{Role: reader}, Privileges: mask},
			{Grantor: third, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege},
		}}
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(original.Entries, retained) {
			t.Fatal("owner ACL collision/provenance/immutability", got, want, original)
		}
	}
	for _, acl := range []ACLValue{{}, {Explicit: true}} {
		if got := schemaACLOwner(acl, old, next); !reflect.DeepEqual(got, acl) {
			t.Fatal("NULL/empty distinction", acl, got)
		}
	}
}
func TestRoleOwnershipReciprocityAndStorage(t *testing.T) {
	e, owner, _, target, reader := ownershipUnitRoles()
	for _, q := range []string{"CREATE TABLE ownership_t(id SERIAL PRIMARY KEY,payload INT)", "CREATE INDEX ownership_idx ON ownership_t(payload)", "INSERT INTO ownership_t(payload) VALUES(7)", "CREATE TYPE ownership_e AS ENUM('a')", "CREATE DOMAIN ownership_d AS INT", "CREATE TYPE ownership_c AS(id INT)"} {
		ownershipUnitExec(t, e, q)
	}
	base := e.CatalogRead()
	table := base.state.descriptors[base.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "ownership_t"}]].(*relationDescriptor)
	// Explicit stable-sub-ID and associated-type ACL payloads are not public
	// GRANT-provider extensions. Install them in the canonical private stage.
	setup := e.BeginCatalogStage(base)
	copy := *table
	copy.acl = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, reader, SelectPrivilege, 0)}}
	copy.columnACL = map[int32]ACLValue{table.attributes[1]: {Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, reader, UpdatePrivilege, UpdatePrivilege)}}}
	copy.revision++
	if err := setup.Replace(&copy); err != nil {
		t.Fatal(err)
	}
	for _, id := range []sqltype.ID{table.rowType, base.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(table.rowType)}].(*typeDescriptor).array} {
		d := base.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}].(*typeDescriptor)
		next := *d
		next.acl = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, reader, UsagePrivilege, 0)}}
		next.revision++
		if err := setup.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	base = e.CatalogRead()
	oldTable := base.state.descriptors[table.address].(*relationDescriptor)
	store := e.storage[table.address]
	rows := store.versions
	generators := make(map[ObjectAddress]*sequenceGenerator)
	for a, g := range e.sequences {
		generators[a] = g
	}
	stage := e.BeginCatalogStage(base)
	for _, name := range []string{"ownership_t", "ownership_e", "ownership_d", "ownership_c"} {
		class := TypeClassID
		if name == "ownership_t" {
			class = RelationClassID
		}
		d := base.state.descriptors[base.state.names[NameKey{ClassID: class, Namespace: table.namespace, Name: name}]]
		if d == nil {
			t.Fatal("missing root", name)
		}
		if err := stageOwnership(stage, d, target); err != nil {
			t.Fatal(name, err)
		}
	}
	ownershipUnitPublish(t, e, stage)
	current := e.CatalogRead()
	compositeAddress := base.state.names[NameKey{ClassID: TypeClassID, Namespace: table.namespace, Name: "ownership_c"}]
	composite := base.state.descriptors[compositeAddress].(*typeDescriptor)
	arrayAddress := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(composite.array)}
	array := base.state.descriptors[arrayAddress].(*typeDescriptor)
	backing := base.state.descriptors[composite.owningRelation].(*relationDescriptor)
	movedComposite := current.state.descriptors[compositeAddress].(*typeDescriptor)
	movedArray := current.state.descriptors[arrayAddress].(*typeDescriptor)
	movedBacking := current.state.descriptors[composite.owningRelation].(*relationDescriptor)
	if !array.typ.IsArray() || backing.rowType != composite.typ.ID() || backing.rowType == array.typ.ID() || movedComposite.owner != target || movedArray.owner != target || movedBacking.owner != target || movedBacking.rowType != backing.rowType || movedComposite.array != composite.array || movedComposite.owningRelation != composite.owningRelation || movedArray.owningRelation != array.owningRelation || composite.owner != owner || array.owner != owner || backing.owner != owner || movedComposite.typ != composite.typ || movedArray.typ != array.typ {
		t.Fatal("composite root/array/backing reciprocity or retained codec mutated")
	}
	for a, old := range base.state.descriptors {
		next := current.state.descriptors[a]
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
	moved := current.state.descriptors[table.address].(*relationDescriptor)
	if moved.owner != target || moved.attributes[1] != oldTable.attributes[1] || moved.acl.Entries[0].Grantor != target || moved.columnACL[moved.attributes[1]].Entries[0].Grantor != target {
		t.Fatal("table/column ACL or stable attribute", moved)
	}
	if e.storage[table.address] != store || len(store.versions) != len(rows) || &store.versions[0] != &rows[0] || oldTable.owner != owner || oldTable.acl.Entries[0].Grantor != owner {
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
	for _, d := range base.state.descriptors {
		switch d.Kind() {
		case DatabaseObject:
			if !ownershipRoot(base, d) {
				t.Fatal("represented database missing from ownership roots", d)
			}
		case NamespaceObject:
			expected := d.Address().ObjectID == PublicNamespaceOID
			if ownershipRoot(base, d) != expected {
				t.Fatal("public exception or protected catalog namespace misclassified", d)
			}
		case RelationObject, TypeObject, CollationObject:
			if ownershipRoot(base, d) {
				t.Fatal("immutable builtin executable ownership root", d)
			}
		}
	}
	ns := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 200000}, name: "ownership_overflow", revision: DefinitionRevision(^uint64(0)), owner: e.roles.names["postgres"]}
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
	if len(stage.descriptors) != 0 || e.state.descriptors[ns.address] != ns {
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
		before, roles := e.state, e.roles
		description, err := e.Describe(op, stmt)
		if err != nil || description.Parameters.Count != 0 || len(description.Parameters.Types) != 0 || len(description.Results) != 0 || e.state != before || e.roles != roles {
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
		before, roles := e.state, e.roles
		if _, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE "+name+" OWNER TO ownership_target"), nil); err == nil {
			t.Fatal("direct provider/backing ownership accepted", name)
		}
		if e.state != before || e.roles != roles {
			t.Fatal("direct provider/backing mutation", name)
		}
	}
}
func TestRoleOwnershipGrantOptionsRespectObjectMask(t *testing.T) {
	e, owner, _, _, reader := ownershipUnitRoles()
	for _, mask := range []Privileges{CreatePrivilege, UsagePrivilege, SelectPrivilege | UpdatePrivilege} {
		if got, err := scopedACLGrantOptions(context.Background(), e.Roles(), ACLValue{}, owner, owner, mask); err != nil || got != mask {
			t.Fatal("implicit ownership leaked foreign-class privilege bits", mask, got, err)
		}
		acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: reader}, Privileges: CreatePrivilege | SelectPrivilege, GrantOptions: CreatePrivilege | SelectPrivilege}}}
		if got, err := scopedACLGrantOptions(context.Background(), e.Roles(), acl, owner, reader, mask); err != nil || got != (CreatePrivilege|SelectPrivilege)&mask {
			t.Fatal("explicit grant options leaked foreign-class privilege bits", mask, got, err)
		}
	}
}

func TestRoleOwnershipInitialSourceCanonicalIdentity(t *testing.T) {
	e := New()
	graph := cloneRoles(e.roles)
	initial := PrincipalRef{e, bootstrapPrincipalID}
	renamed := graph.roles[initial]
	renamed.Name = "renamed_initial"
	graph.roles[initial] = renamed
	delete(graph.names, "postgres")
	graph.names[renamed.Name] = initial
	ordinary := PrincipalRef{e, e.nextPrincipal}
	graph.roles[ordinary] = RoleDescriptor{Ref: ordinary, Name: "postgres", Superuser: true, Inherit: true}
	graph.names["postgres"] = ordinary
	auth := catalogAuthorization(e, e.CatalogRead())
	auth.roles = RoleRead{e, graph, auth.catalog}
	retained := e.roles
	for _, reassign := range []bool{false, true} {
		refs, protected, err := ownedSources(auth, []parser.RoleSpec{{Name: "postgres"}, {Name: "renamed_initial"}, {Name: "renamed_initial"}}, reassign)
		if err != nil || len(refs) != 2 || !refs[ordinary] || !refs[initial] || protected != initial {
			t.Fatal("canonical initial source or deduplicated union", refs, protected, err)
		}
		refs, protected, err = ownedSources(auth, []parser.RoleSpec{{Name: "postgres"}}, reassign)
		if err != nil || len(refs) != 1 || protected != (PrincipalRef{}) {
			t.Fatal("ordinary familiar-name superuser was protected", refs, protected, err)
		}
		if _, _, err = ownedSources(auth, []parser.RoleSpec{{Name: "renamed_initial"}, {Name: "missing_source"}}, reassign); err == nil {
			t.Fatal("initial protection masked late operand resolution")
		}
	}
	if graph.roles[initial].predefined || e.roles != retained || e.roles.roles[initial].Name != "postgres" {
		t.Fatal("protection changed unrelated predefined flag or live initial identity")
	}
}
