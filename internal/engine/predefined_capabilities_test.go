package engine

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPredefinedCapabilityMasks(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	reader := roleAttributeCreate(t, e, op, "capability_reader", false, parser.RoleOptions{})
	for _, role := range []string{"pg_read_all_data", "pg_write_all_data", "pg_maintain"} {
		roleAttributeApply(t, e, op, predefinedMembership(role, reader.Name))
	}
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	// Private immutable descriptors isolate class masks from public SQL syntax.
	descriptors := []struct {
		d    catalog.Descriptor
		want authorization.Privileges
	}{
		{&catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 100001}, ObjectName: "private", Owner: owner, ACL: authorization.ACLValue{Explicit: true}}, authorization.UsagePrivilege},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100002}, ObjectName: "table", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.TableRelation, Attributes: []int32{1}}, authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.MaintainPrivilege},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100003}, ObjectName: "view", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.ViewRelation}, authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.MaintainPrivilege},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100004}, ObjectName: "materialized", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.MaterializedViewRelation}, authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.MaintainPrivilege},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100005}, ObjectName: "sequence", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.SequenceRelation}, authorization.SelectPrivilege | authorization.UpdatePrivilege},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100006}, ObjectName: "index", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.IndexRelation}, 0},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100007}, ObjectName: "catalog", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.CatalogRelation}, authorization.SelectPrivilege},
		{&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100008}, ObjectName: "provider", Owner: owner, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, RelationKind: catalog.ViewRelation, VirtualView: true}, authorization.SelectPrivilege},
	}
	state := catalog.CloneState(e.catalog.state)
	for _, tc := range descriptors {
		state.SetDescriptor(tc.d.Address(), tc.d)
	}
	v := AuthorizationView{engine: e, pairedCatalog: e.catalog.read(state), roleRecord: e.roles, catalog: e.catalog.read(state), roles: e.roleRead(e.catalog.read(state), e.roles), state: authorization.RoleState{CurrentUser: reader.Ref}}
	for _, tc := range descriptors {
		ref := e.catalog.Domain.Ref(state.Epoch(), tc.d.Address())
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
	typ := state.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25})
	if predefinedPrivileges(typ, []authorization.PrincipalRef{e.authorizationDomain.Principal(6181), e.authorizationDomain.Principal(6182), e.authorizationDomain.Principal(6337)}) != 0 {
		t.Fatal("data capability leaked onto type")
	}
	column := e.catalog.Domain.Ref(state.Epoch(), descriptors[1].d.Address())
	column.Address.SubID = 1
	if got, err := v.Effective(column, false); err != nil || got != authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege {
		t.Fatal("column mask", got, err)
	}
	// An ordinary ACL supplements predefined capabilities; revocation changes
	// only fresh role views, never retained descriptor or authority snapshots.
	table := descriptors[1].d.(*catalog.Relation)
	withColumn := *table
	withColumn.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ReferencesPrivilege, GrantOptions: authorization.ReferencesPrivilege}}}}
	withColumnState := catalog.CloneState(state)
	withColumnState.SetDescriptor(table.Address(), &withColumn)
	v.catalog = e.catalog.read(withColumnState)
	v.recaptureCatalog(v.catalog)
	if got, err := v.Effective(column, false); err != nil || got != authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege {
		t.Fatal("ACL plus capability", got, err)
	}
	if got, err := v.Effective(column, true); err != nil || got != authorization.ReferencesPrivilege {
		t.Fatal("ordinary options", got, err)
	}
	for _, role := range []string{"pg_read_all_data", "pg_write_all_data", "pg_maintain"} {
		revoke := predefinedMembership(role, reader.Name)
		revoke.Revoke = true
		roleAttributeApply(t, e, op, revoke)
	}
	fresh := v
	fresh.roleRecord = e.roles
	fresh.roles = e.roleRead(fresh.catalog, e.roles)
	if got, err := fresh.Effective(e.catalog.Domain.Ref(state.Epoch(), table.Address()), false); err != nil || got != 0 {
		t.Fatal("fresh authority retained capability", got, err)
	}
	if got, err := v.Effective(e.catalog.Domain.Ref(state.Epoch(), table.Address()), false); err != nil || got != descriptors[1].want {
		t.Fatal("retained authority changed", got, err)
	}
	for _, ref := range []ids.ObjectRef{New().catalog.Domain.Ref(state.Epoch(), table.Address()), e.catalog.Domain.Ref(state.Epoch()+1, table.Address()), e.catalog.Domain.Ref(state.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999})} {
		if _, err := v.Effective(ref, false); err == nil {
			t.Fatal("capability accepted invalid object", ref)
		}
	}
	fresh.state.CurrentUser = New().authorizationDomain.Principal(reader.Ref.ID())
	if _, err := fresh.Effective(e.catalog.Domain.Ref(state.Epoch(), table.Address()), false); err == nil {
		t.Fatal("capability accepted foreign actor")
	}
	roleAttributeApply(t, e, op, &parser.DropRole{Names: []string{reader.Name}})
	fresh.roles = e.Roles()
	fresh.roleRecord = e.roles
	fresh.state.CurrentUser = reader.Ref
	if _, err := fresh.Effective(e.catalog.Domain.Ref(state.Epoch(), table.Address()), false); err == nil {
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
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "maintained_index"})
	index := e.catalog.state.MustRelation(address)
	root, storage := e.storage.IndexRoots[address], e.storage.Relations[index.SQLIndex.Parent]
	if root == nil || storage == nil {
		t.Fatal("missing real index or parent storage")
	}
	utility := &parser.Reindex{Kind: parser.Identifier{Text: "index"}, Name: predefinedObjectName("maintained_index")}
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	roleAttributeFailure(t, e, op, utility, "42501")
	if e.storage.IndexRoots[address] != root {
		t.Fatal("denied rebuild replaced root")
	}
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Default: true})
	roleAttributeApply(t, e, op, predefinedMembership("pg_maintain", actor.Name))
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	roleAttributeApply(t, e, op, utility)
	next := e.storage.IndexRoots[address]
	if next == root || !reflect.DeepEqual(next.Entries, root.Entries) || e.catalog.state.Get(address) != index || e.storage.Relations[index.SQLIndex.Parent] != storage {
		t.Fatal("MAINTAIN did not rebuild real isolated root")
	}
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Default: true})
	revoke := predefinedMembership("pg_maintain", actor.Name)
	revoke.Revoke = true
	roleAttributeApply(t, e, op, revoke)
	roleAttributeApply(t, e, op, &parser.SessionAuthorization{Name: actor.Name})
	roleAttributeFailure(t, e, op, utility, "42501")
	if e.storage.IndexRoots[address] != next {
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
	sourceEdges := append([]authorization.Membership(nil), fixtureEdges(roles)...)
	stage := e.restoreStage()
	expectedEdges := append([]authorization.Membership(nil), sourceEdges...)
	for i := range expectedEdges {
		expectedEdges[i].Role = stage.authorizationDomain.Principal(expectedEdges[i].Role.ID())
		expectedEdges[i].Member = stage.authorizationDomain.Principal(expectedEdges[i].Member.ID())
		expectedEdges[i].Grantor = stage.authorizationDomain.Principal(expectedEdges[i].Grantor.ID())
	}
	if !reflect.DeepEqual(stage.Roles().Memberships(), expectedEdges) {
		t.Fatal("import lost captured target graph/options/provenance")
	}
	if e.roles != roles || !reflect.DeepEqual(sourceEdges, fixtureEdges(roles)) {
		t.Fatal("import mutated retained target graph")
	}
	closure, err := stage.Roles().RoleClosure(context.Background(), stage.authorizationDomain.Principal(10), true)
	if err != nil || !reflect.DeepEqual(closure, []authorization.PrincipalRef{stage.authorizationDomain.Principal(10), stage.authorizationDomain.Principal(6171)}) {
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
		public := e.catalog.state.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
		if public.Owner != (e.authorizationDomain.Principal(6171)) || !bootstrapNamespaceDumpable(public) {
			t.Fatal("public owner or ACL baseline", public)
		}
		for _, entry := range public.ACL.Entries {
			if entry.Grantor != public.Owner || !entry.Grantee.Public && entry.Grantee.Role != public.Owner {
				t.Fatal("public ACL provenance", entry)
			}
		}
	}
	target := New()
	if _, err := target.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	public := target.catalog.state.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if public.Owner != (target.authorizationDomain.Principal(6171)) {
		t.Fatal("foreign public owner escaped remapping", public.Owner)
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
		beforeCatalog, beforeRoles := e.catalog.state, e.roles
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
		if e.catalog.state != beforeCatalog || e.roles != beforeRoles {
			t.Fatal("use-only capability published a grant")
		}
	}
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "grant_target"})
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
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
