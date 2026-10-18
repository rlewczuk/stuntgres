package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgClassUnitSource(t *testing.T, e *Engine) *relationDescriptor {
	t.Helper()
	return e.CatalogRead().state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 1259}].(*relationDescriptor)
}

func TestPgClassProjection(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	source := pgClassUnitSource(t, e)
	names := []string{"oid", "relname", "relnamespace", "reltype", "reloftype", "relowner", "relam", "relfilenode", "reltablespace", "relpages", "reltuples", "relallvisible", "relallfrozen", "reltoastrelid", "relhasindex", "relisshared", "relpersistence", "relkind", "relnatts", "relchecks", "relhasrules", "relhastriggers", "relhassubclass", "relrowsecurity", "relforcerowsecurity", "relispopulated", "relreplident", "relispartition", "relrewrite", "relfrozenxid", "relminmxid", "relacl", "reloptions", "relpartbound"}
	typeIDs := []sqltype.ID{26, 19, 26, 26, 26, 26, 26, 26, 26, 23, 700, 23, 23, 26, 16, 16, 18, 18, 21, 21, 16, 16, 16, 16, 16, 16, 18, 16, 26, 28, 28, 1034, 1009, 194}
	if len(source.columns) != 34 || source.attributeHighWater != 34 {
		t.Fatal("full catalog schema", source.columns, source.attributeHighWater)
	}
	for i, column := range source.columns {
		if column.Name != names[i] || column.Usage.Type.ID() != typeIDs[i] || !column.NullabilityKnown || column.Nullable != (i >= 31) || column.OriginTable != 1259 || column.OriginAttribute != int16(i+1) {
			t.Fatal("descriptor", i, column)
		}
	}
	relation := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 200001}, namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, name: "projection", owner: e.authorizationDomain.Principal(10), rowType: 200002, ofType: ObjectAddress{ClassID: TypeClassID, ObjectID: 200003}, attributeHighWater: 2, hasIndexes: true, hasSubclasses: true, rlsEnabled: true, rlsForced: true, checkConstraints: make([]checkConstraint, 2)}
	row, err := e.pgClassRow(context.Background(), catalog, e.Roles(), source, relation)
	if err != nil || len(row) != 34 {
		t.Fatal(row, err)
	}
	for i, value := range row {
		if value.Type() != source.columns[i].Usage.Type || value.IsNull() != (i >= 31) {
			t.Fatal("value carrier", i, value)
		}
	}
	for i, want := range map[int]uint32{0: 200001, 2: uint32(PublicNamespaceOID), 3: 200002, 4: 200003, 5: 10, 6: 2, 7: 0, 8: 0, 13: 0, 28: 0} {
		got, err := row[i].OID()
		if err != nil || got != want {
			t.Fatal("OID", i, got, want, err)
		}
	}
	for i, want := range map[int]int64{9: 0, 11: 0, 12: 0, 18: 2, 19: 2} {
		got, err := row[i].Integer()
		if err != nil || got != want {
			t.Fatal("integer", i, got, want, err)
		}
	}
	for i, want := range map[int]bool{14: true, 15: false, 20: false, 21: false, 22: true, 23: true, 24: true, 25: true, 27: false} {
		got, err := row[i].Boolean()
		if err != nil || got != want {
			t.Fatal("boolean", i, got, want, err)
		}
	}
	for i, want := range map[int]string{1: "projection", 16: "p", 17: "r", 26: "d"} {
		got, err := row[i].Text()
		if err != nil || got != want {
			t.Fatal("text", i, got, want, err)
		}
	}
	if n, err := row[10].Float(); err != nil || n != -1 {
		t.Fatal("statistics are unknown", n, err)
	}
	for _, i := range []int{29, 30} {
		if n, err := row[i].XID(); err != nil || n != 0 {
			t.Fatal("horizon", i, n, err)
		}
		if _, err := row[i].OID(); err == nil {
			t.Fatal("XID became OID")
		}
	}
	for _, oid := range []OID{1213, 1262, 2396} {
		d := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
		row, err := e.pgClassRow(context.Background(), catalog, e.Roles(), source, d)
		if err != nil {
			t.Fatal(err)
		}
		shared, _ := row[15].Boolean()
		space, _ := row[8].OID()
		if !shared || space != 1664 {
			t.Fatal("shared compatibility metadata", oid, shared, space)
		}
	}
}

func TestPgClassKinds(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	for _, tc := range []struct {
		kind relationKind
		want string
	}{{tableRelation, "r"}, {sequenceRelation, "S"}, {viewRelation, "v"}, {materializedViewRelation, "m"}, {compositeRelation, "c"}, {partitionedRelation, "p"}} {
		if got := pgClassKind(catalog, &relationDescriptor{relationKind: tc.kind}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, oid := range []OID{12000, 12001, 12002, 12003, 12006, 12007, 12008, 12014, 12081} {
		d := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: oid}].(*relationDescriptor)
		before := d.virtualView
		if got := pgClassKind(catalog, d); got != "v" || d.virtualView != before {
			t.Fatal("classification changed dispatch", oid, got)
		}
	}
	parentAddress := ObjectAddress{ClassID: RelationClassID, ObjectID: 200010}
	state := cloneCatalog(catalog.state)
	state.descriptors[parentAddress] = &relationDescriptor{address: parentAddress, partition: &partitionDefinition{}}
	index := &relationDescriptor{relationKind: indexRelation, sqlIndex: &indexDefinition{parent: parentAddress}}
	if got := pgClassKind(CatalogRead{e, state}, index); got != "I" {
		t.Fatal(got)
	}
	delete(state.descriptors, parentAddress)
	if got := pgClassKind(CatalogRead{e, state}, index); got != "i" {
		t.Fatal(got)
	}
}

func TestPgClassACLTranslation(t *testing.T) {
	e := New()
	e.nextPrincipal = 100001
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE acl_translation_grantor")
	roles := e.Roles()
	array := pgClassUnitSource(t, e).columns[31].Usage.Type
	owner := e.authorizationDomain.Principal(10)
	for _, tc := range []struct {
		privilege authorization.Privileges
		mask      uint32
	}{
		{authorization.InsertPrivilege, 1}, {authorization.SelectPrivilege, 2}, {authorization.UpdatePrivilege, 4}, {authorization.DeletePrivilege, 8}, {authorization.TruncatePrivilege, 16}, {authorization.ReferencesPrivilege, 32}, {authorization.TriggerPrivilege, 64}, {authorization.ExecutePrivilege, 128}, {authorization.UsagePrivilege, 256}, {authorization.CreatePrivilege, 512}, {authorization.MaintainPrivilege, 16384},
	} {
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: tc.privilege, GrantOptions: tc.privilege}}}
		retained := append([]authorization.ACLEntry(nil), acl.Entries...)
		value, err := e.pgClassACL(roles, array, acl)
		if err != nil {
			t.Fatal(err)
		}
		authority, grantee, grantor, mask, options, err := value.ArrayElement(0).ACLItem()
		if err != nil || authority != &e.aclAuthority || grantee != 0 || grantor != 10 || mask != tc.mask || options != tc.mask || !reflect.DeepEqual(acl.Entries, retained) {
			t.Fatal(tc, grantee, grantor, mask, options, err)
		}
	}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.SelectPrivilege | authorization.UpdatePrivilege, GrantOptions: authorization.UpdatePrivilege}}}
	value, err := e.pgClassACL(roles, array, acl)
	if err != nil {
		t.Fatal(err)
	}
	_, grantee, _, mask, options, err := value.ArrayElement(0).ACLItem()
	if err != nil || grantee != 10 || mask != 6 || options != 4 {
		t.Fatal("independent grant-option mask", grantee, mask, options, err)
	}
	for _, explicit := range []bool{false, true} {
		v, err := e.pgClassACL(roles, array, authorization.ACLValue{Explicit: explicit})
		if err != nil || v.IsNull() == explicit || v.ArrayLen() != 0 {
			t.Fatal("NULL vs empty", explicit, v, err)
		}
	}
	foreign := &authorization.Domain{}
	acl.Entries[0].Grantor = foreign.Principal(10)
	if _, err := e.pgClassACL(roles, array, acl); err == nil {
		t.Fatal("foreign grantor admitted")
	}
	acl.Entries[0].Grantor = owner
	acl.Entries[0].Grantee.Role = foreign.Principal(10)
	if _, err := e.pgClassACL(roles, array, acl); err == nil {
		t.Fatal("foreign grantee admitted")
	}
	acl.Entries[0].Grantee.Role = owner
	acl.Entries[0].GrantOptions = authorization.InsertPrivilege
	if _, err := e.pgClassACL(roles, array, acl); err == nil {
		t.Fatal("option absent privilege admitted")
	}
	all := authorization.Privileges((1 << 11) - 1)
	combined := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: all, GrantOptions: authorization.UpdatePrivilege},
		{Grantor: roles.LookupRef("acl_translation_grantor"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: all, GrantOptions: all},
	}}
	value, err = e.pgClassACL(roles, array, combined)
	if err != nil {
		t.Fatal(err)
	}
	if value.ArrayLen() != 2 {
		t.Fatal("stored order/cardinality", value.ArrayLen())
	}
	for i, want := range []struct{ grantee, grantor, options uint32 }{{10, 10, 4}, {0, 100001, 17407}} {
		authority, grantee, grantor, mask, options, err := value.ArrayElement(i).ACLItem()
		if err != nil || authority != &e.aclAuthority || grantee != want.grantee || grantor != want.grantor || mask != 17407 || options != want.options {
			t.Fatal("combined masks/order/principals", i, grantee, grantor, mask, options, err)
		}
	}
}
