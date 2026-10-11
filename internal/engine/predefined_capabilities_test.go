package engine

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPredefinedCapabilityMasks(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	reader := roleAttributeCreate(t, e, op, "capability_reader", false, parser.RoleOptions{})
	for _, role := range []string{"pg_read_all_data", "pg_write_all_data", "pg_maintain"} {
		roleAttributeApply(t, e, op, predefinedMembership(role, reader.Name))
	}
	owner := e.roles.names["postgres"]
	// Private immutable descriptors isolate class masks from public SQL syntax.
	descriptors := []struct {
		d    Descriptor
		want Privileges
	}{
		{&namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 100001}, name: "private", owner: owner, acl: ACLValue{Explicit: true}}, UsagePrivilege},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100002}, name: "table", owner: owner, acl: ACLValue{Explicit: true}, relationKind: tableRelation, attributes: []int32{1}}, SelectPrivilege | InsertPrivilege | UpdatePrivilege | DeletePrivilege | MaintainPrivilege},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100003}, name: "view", owner: owner, acl: ACLValue{Explicit: true}, relationKind: viewRelation}, SelectPrivilege | InsertPrivilege | UpdatePrivilege | DeletePrivilege | MaintainPrivilege},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100004}, name: "materialized", owner: owner, acl: ACLValue{Explicit: true}, relationKind: materializedViewRelation}, SelectPrivilege | InsertPrivilege | UpdatePrivilege | DeletePrivilege | MaintainPrivilege},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100005}, name: "sequence", owner: owner, acl: ACLValue{Explicit: true}, relationKind: sequenceRelation}, SelectPrivilege | UpdatePrivilege},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100006}, name: "index", owner: owner, acl: ACLValue{Explicit: true}, relationKind: indexRelation}, 0},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100007}, name: "catalog", owner: owner, acl: ACLValue{Explicit: true}, relationKind: catalogRelation}, SelectPrivilege},
		{&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 100008}, name: "provider", owner: owner, acl: ACLValue{Explicit: true}, relationKind: viewRelation, virtualView: true}, SelectPrivilege},
	}
	state := cloneCatalog(e.state)
	for _, tc := range descriptors {
		state.descriptors[tc.d.Address()] = tc.d
	}
	v := AuthorizationView{engine: e, catalog: CatalogRead{e, state}, roles: e.Roles(), state: RoleState{CurrentUser: reader.Ref}}
	for _, tc := range descriptors {
		ref := ObjectRef{e.owner, state.epoch, tc.d.Address()}
		got, err := v.Effective(ref, false)
		if err != nil || got != tc.want {
			t.Fatal(tc.d.Name(), got, tc.want, err)
		}
		options, err := v.Effective(ref, true)
		if err != nil || options != 0 {
			t.Fatal("capability became grant option", tc.d.Name(), options, err)
		}
		if err := v.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err == nil {
			t.Fatal("capability became ownership", tc.d.Name())
		}
	}
	typ := state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 25}]
	if predefinedPrivileges(typ, []PrincipalRef{{e, 6181}, {e, 6182}, {e, 6337}}) != 0 {
		t.Fatal("data capability leaked onto type")
	}
	column := ObjectRef{e.owner, state.epoch, descriptors[1].d.Address()}
	column.Address.SubID = 1
	if got, err := v.Effective(column, false); err != nil || got != SelectPrivilege|InsertPrivilege|UpdatePrivilege {
		t.Fatal("column mask", got, err)
	}
	// An ordinary ACL supplements predefined capabilities; revocation changes
	// only fresh role views, never retained descriptor or authority snapshots.
	table := descriptors[1].d.(*relationDescriptor)
	withColumn := *table
	withColumn.columnACL = map[int32]ACLValue{1: {Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: ReferencesPrivilege, GrantOptions: ReferencesPrivilege}}}}
	withColumnState := cloneCatalog(state)
	withColumnState.descriptors[table.address] = &withColumn
	v.catalog = CatalogRead{e, withColumnState}
	if got, err := v.Effective(column, false); err != nil || got != SelectPrivilege|InsertPrivilege|UpdatePrivilege|ReferencesPrivilege {
		t.Fatal("ACL plus capability", got, err)
	}
	if got, err := v.Effective(column, true); err != nil || got != ReferencesPrivilege {
		t.Fatal("ordinary options", got, err)
	}
	for _, role := range []string{"pg_read_all_data", "pg_write_all_data", "pg_maintain"} {
		revoke := predefinedMembership(role, reader.Name)
		revoke.Revoke = true
		roleAttributeApply(t, e, op, revoke)
	}
	fresh := v
	fresh.roles = e.Roles()
	if got, err := fresh.Effective(ObjectRef{e.owner, state.epoch, table.address}, false); err != nil || got != 0 {
		t.Fatal("fresh authority retained capability", got, err)
	}
	if got, err := v.Effective(ObjectRef{e.owner, state.epoch, table.address}, false); err != nil || got != descriptors[1].want {
		t.Fatal("retained authority changed", got, err)
	}
	for _, ref := range []ObjectRef{{New().owner, state.epoch, table.address}, {e.owner, state.epoch + 1, table.address}, {e.owner, state.epoch, ObjectAddress{ClassID: RelationClassID, ObjectID: 999999}}} {
		if _, err := v.Effective(ref, false); err == nil {
			t.Fatal("capability accepted invalid object", ref)
		}
	}
	fresh.state.CurrentUser = PrincipalRef{New(), reader.Ref.id}
	if _, err := fresh.Effective(ObjectRef{e.owner, state.epoch, table.address}, false); err == nil {
		t.Fatal("capability accepted foreign actor")
	}
	roleAttributeApply(t, e, op, &parser.DropRole{Names: []string{reader.Name}})
	fresh.roles = e.Roles()
	fresh.state.CurrentUser = reader.Ref
	if _, err := fresh.Effective(ObjectRef{e.owner, state.epoch, table.address}, false); err == nil {
		t.Fatal("capability accepted retired actor")
	}
}

func predefinedObjectName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}

func TestPredefinedMaintainRebuild(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, op, "rebuilder", true, parser.RoleOptions{})
	roleAttributeApply(t, e, op, &parser.CreateTable{Name: predefinedObjectName("maintained"), Elements: []parser.TableElement{{Column: &parser.Column{Name: "n", TypeName: parser.TypeName{Name: "integer"}}}}})
	roleAttributeApply(t, e, op, &parser.CreateIndex{Name: parser.Identifier{Text: "maintained_index"}, Table: predefinedObjectName("maintained"), Method: "btree", Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "n"}}}})
	address := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "maintained_index"}]
	index := e.state.descriptors[address].(*relationDescriptor)
	root, storage := e.indexRoots[address], e.storage[index.sqlIndex.parent]
	if root == nil || storage == nil {
		t.Fatal("missing real index or parent storage")
	}
	utility := &parser.Reindex{Kind: parser.Identifier{Text: "index"}, Name: predefinedObjectName("maintained_index")}
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	roleAttributeFailure(t, e, op, utility, "42501")
	if e.indexRoots[address] != root {
		t.Fatal("denied rebuild replaced root")
	}
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Default: true})
	roleAttributeApply(t, e, op, predefinedMembership("pg_maintain", actor.Name))
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	roleAttributeApply(t, e, op, utility)
	next := e.indexRoots[address]
	if next == root || !reflect.DeepEqual(next.entries, root.entries) || e.state.descriptors[address] != index || e.storage[index.sqlIndex.parent] != storage {
		t.Fatal("MAINTAIN did not rebuild real isolated root")
	}
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Default: true})
	revoke := predefinedMembership("pg_maintain", actor.Name)
	revoke.Revoke = true
	roleAttributeApply(t, e, op, revoke)
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	roleAttributeFailure(t, e, op, utility, "42501")
	if e.indexRoots[address] != next {
		t.Fatal("revoked rebuild replaced root")
	}
}

func TestPredefinedRoleControls(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	revoke := predefinedMembership("pg_read_all_settings", "pg_monitor")
	revoke.Revoke = true
	roleAttributeApply(t, e, op, revoke)
	roles := e.roles
	stage := e.restoreStage()
	if len(stage.Roles().Memberships()) != 0 {
		t.Fatal("import cloned target graph")
	}
	closure, err := stage.Roles().roleClosure(context.Background(), PrincipalRef{stage, 10}, true)
	if err != nil || !reflect.DeepEqual(closure, []PrincipalRef{{stage, 10}, {stage, 6171}}) {
		t.Fatal("stage implicit owner", closure, err)
	}
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "CREATE ROLE") || strings.Contains(dump.String(), "GRANT pg_") || strings.Contains(dump.String(), "OWNER TO postgres") {
		t.Fatal("dump invented shared authority or wrong public baseline", dump.String())
	}
	for _, control := range []func() error{
		func() error { return e.Reset(context.Background()) },
		func() error { return e.Restore(context.Background(), bytes.NewReader(dump.Bytes())) },
	} {
		if err := control(); err != nil {
			t.Fatal(err)
		}
		if e.roles != roles || len(e.Roles().Memberships()) != 2 {
			t.Fatal("control reseeded shared graph")
		}
		public := e.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}].(*namespaceDescriptor)
		if public.owner != (PrincipalRef{e, 6171}) || !bootstrapNamespaceDumpable(public) {
			t.Fatal("public owner or ACL baseline", public)
		}
		for _, entry := range public.acl.Entries {
			if entry.Grantor != public.owner || !entry.Grantee.Public && entry.Grantee.Role != public.owner {
				t.Fatal("public ACL provenance", entry)
			}
		}
	}
	target := New()
	if _, err := target.ReplaceCatalog(context.Background(), e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	public := target.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}].(*namespaceDescriptor)
	if public.owner != (PrincipalRef{target, 6171}) {
		t.Fatal("foreign public owner escaped remapping", public.owner)
	}
	if len(target.Roles().Memberships()) != 3 {
		t.Fatal("import replaced independent target graph")
	}
}

func TestPredefinedCapabilityGrantSource(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, op, "grant_actor", false, parser.RoleOptions{})
	recipient := roleAttributeCreate(t, e, op, "grant_recipient", false, parser.RoleOptions{})
	roleAttributeApply(t, e, op, &parser.CreateTable{Name: predefinedObjectName("grant_target"), Elements: []parser.TableElement{{Column: &parser.Column{Name: "n", TypeName: parser.TypeName{Name: "integer"}}}}})
	roleAttributeApply(t, e, op, predefinedMembership("pg_read_all_data", actor.Name))
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	op.DetachNotices()
	for _, columns := range [][]parser.Identifier{nil, {{Text: "n"}}} {
		beforeCatalog, beforeRoles := e.state, e.roles
		roleAttributeApply(t, e, op, &parser.ObjectGrant{
			Relations:   []parser.ObjectName{predefinedObjectName("grant_target")},
			Privileges:  []parser.GrantPrivilege{{Name: "select", Columns: columns}},
			Grantees:    []parser.GrantGrantee{{Role: parser.RoleSpec{Name: recipient.Name}}},
			GrantOption: true,
		})
		message := `no privileges were granted for "grant_target"`
		if len(columns) != 0 {
			message = `no privileges were granted for column "n" of relation "grant_target"`
		}
		want := []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: message}}
		if notices := op.DetachNotices(); !reflect.DeepEqual(notices, want) {
			t.Fatal("use-only grant warning", notices, want)
		}
		if e.state != beforeCatalog || e.roles != beforeRoles {
			t.Fatal("use-only capability published a grant")
		}
	}
	address := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "grant_target"}]
	ref := ObjectRef{e.owner, e.state.epoch, address}
	view, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if rights, err := view.Effective(ref, true); err != nil || rights != 0 {
		t.Fatal("capability gained options", rights, err)
	}
	view.state.CurrentUser = recipient.Ref
	if rights, err := view.Effective(ref, false); err != nil || rights != 0 {
		t.Fatal("recipient gained rights", rights, err)
	}
	ref.Address.SubID = 1
	if rights, err := view.Effective(ref, false); err != nil || rights != 0 {
		t.Fatal("recipient gained column rights", rights, err)
	}
}
