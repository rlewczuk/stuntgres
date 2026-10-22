package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgClassUnitSource(t *testing.T, e *Engine) *catalog.Relation {
	t.Helper()
	return e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
}

func TestPgClassProjection(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	source := pgClassUnitSource(t, e)
	names := []string{"oid", "relname", "relnamespace", "reltype", "reloftype", "relowner", "relam", "relfilenode", "reltablespace", "relpages", "reltuples", "relallvisible", "relallfrozen", "reltoastrelid", "relhasindex", "relisshared", "relpersistence", "relkind", "relnatts", "relchecks", "relhasrules", "relhastriggers", "relhassubclass", "relrowsecurity", "relforcerowsecurity", "relispopulated", "relreplident", "relispartition", "relrewrite", "relfrozenxid", "relminmxid", "relacl", "reloptions", "relpartbound"}
	typeIDs := []sqltype.ID{26, 19, 26, 26, 26, 26, 26, 26, 26, 23, 700, 23, 23, 26, 16, 16, 18, 18, 21, 21, 16, 16, 16, 16, 16, 16, 18, 16, 26, 28, 28, 1034, 1009, 194}
	if len(source.Columns) != 34 || source.ColumnMeta.HighWater != 34 {
		t.Fatal("full catalog schema", source.Columns, source.ColumnMeta.HighWater)
	}
	for i, column := range source.Columns {
		if column.Name != names[i] || column.Usage.Type.ID() != typeIDs[i] || !column.NullabilityKnown || column.Nullable != (i >= 31) || column.OriginTable != 1259 || column.OriginAttribute != int16(i+1) {
			t.Fatal("descriptor", i, column)
		}
	}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "projection", Owner: e.roles.domain.Principal(10), RowType: 200002, OfType: ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 200003}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, History: catalog.PublicationHistory{HasIndexes: true, HasSubclasses: true}, Security: catalog.RelationSecurity{RLSEnabled: true, RLSForced: true}, Constraints: catalog.RelationConstraints{Check: make([]catalog.CheckConstraint, 2)}}
	row, err := pgcatalog.ClassRow(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Owner: e.catalog.owner(), Source: source, Runtime: engineCatalogRuntime{engine: e}}, relation)
	if err != nil || len(row) != 34 {
		t.Fatal(row, err)
	}
	for i, value := range row {
		if value.Type() != source.Columns[i].Usage.Type || value.IsNull() != (i >= 31) {
			t.Fatal("value carrier", i, value)
		}
	}
	for i, want := range map[int]uint32{0: 200001, 2: uint32(ids.PublicNamespaceOID), 3: 200002, 4: 200003, 5: 10, 6: 2, 7: 0, 8: 0, 13: 0, 28: 0} {
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
	for _, oid := range []ids.OID{1213, 1262, 2396} {
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		row, err := pgcatalog.ClassRow(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Owner: e.catalog.owner(), Source: source, Runtime: engineCatalogRuntime{engine: e}}, d)
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
	view := e.CatalogRead()
	for _, tc := range []struct {
		kind catalog.RelationKind
		want string
	}{{catalog.TableRelation, "r"}, {catalog.SequenceRelation, "S"}, {catalog.ViewRelation, "v"}, {catalog.MaterializedViewRelation, "m"}, {catalog.CompositeRelation, "c"}, {catalog.PartitionedRelation, "p"}} {
		if got := pgcatalog.ClassKind(view, &catalog.Relation{RelationKind: tc.kind}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, oid := range []ids.OID{12000, 12001, 12002, 12003, 12006, 12007, 12008, 12014, 12081} {
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		before := d.VirtualView
		if got := pgcatalog.ClassKind(view, d); got != "v" || d.VirtualView != before {
			t.Fatal("classification changed dispatch", oid, got)
		}
	}
	parentAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200010}
	state := catalog.CloneState(view.State())
	state.SetDescriptor(parentAddress, &catalog.Relation{ObjectAddress: parentAddress, Partition: &catalog.PartitionDefinition{}})
	index := &catalog.Relation{RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parentAddress}}
	if got := pgcatalog.ClassKind(e.catalog.read(state), index); got != "I" {
		t.Fatal(got)
	}
	state.RemoveDescriptor(parentAddress)
	if got := pgcatalog.ClassKind(e.catalog.read(state), index); got != "i" {
		t.Fatal(got)
	}
}

func TestPgClassACLTranslation(t *testing.T) {
	e := New()
	e.roles.nextPrincipal = 100001
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE acl_translation_grantor")
	roles := e.Roles()
	array := pgClassUnitSource(t, e).Columns[31].Usage.Type
	owner := e.roles.domain.Principal(10)
	for _, tc := range []struct {
		privilege authorization.Privileges
		mask      uint32
	}{
		{authorization.InsertPrivilege, 1}, {authorization.SelectPrivilege, 2}, {authorization.UpdatePrivilege, 4}, {authorization.DeletePrivilege, 8}, {authorization.TruncatePrivilege, 16}, {authorization.ReferencesPrivilege, 32}, {authorization.TriggerPrivilege, 64}, {authorization.ExecutePrivilege, 128}, {authorization.UsagePrivilege, 256}, {authorization.CreatePrivilege, 512}, {authorization.MaintainPrivilege, 16384},
	} {
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: tc.privilege, GrantOptions: tc.privilege}}}
		retained := append([]authorization.ACLEntry(nil), acl.Entries...)
		value, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, acl)
		if err != nil {
			t.Fatal(err)
		}
		authority, grantee, grantor, mask, options, err := value.ArrayElement(0).ACLItem()
		if err != nil || authority != &e.roles.aclAuthority || grantee != 0 || grantor != 10 || mask != tc.mask || options != tc.mask || !reflect.DeepEqual(acl.Entries, retained) {
			t.Fatal(tc, grantee, grantor, mask, options, err)
		}
	}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.SelectPrivilege | authorization.UpdatePrivilege, GrantOptions: authorization.UpdatePrivilege}}}
	value, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, acl)
	if err != nil {
		t.Fatal(err)
	}
	_, grantee, _, mask, options, err := value.ArrayElement(0).ACLItem()
	if err != nil || grantee != 10 || mask != 6 || options != 4 {
		t.Fatal("independent grant-option mask", grantee, mask, options, err)
	}
	for _, explicit := range []bool{false, true} {
		v, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, authorization.ACLValue{Explicit: explicit})
		if err != nil || v.IsNull() == explicit || v.ArrayLen() != 0 {
			t.Fatal("NULL vs empty", explicit, v, err)
		}
	}
	foreign := &authorization.Domain{}
	acl.Entries[0].Grantor = foreign.Principal(10)
	if _, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, acl); err == nil {
		t.Fatal("foreign grantor admitted")
	}
	acl.Entries[0].Grantor = owner
	acl.Entries[0].Grantee.Role = foreign.Principal(10)
	if _, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, acl); err == nil {
		t.Fatal("foreign grantee admitted")
	}
	acl.Entries[0].Grantee.Role = owner
	acl.Entries[0].GrantOptions = authorization.InsertPrivilege
	if _, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, acl); err == nil {
		t.Fatal("option absent privilege admitted")
	}
	all := authorization.Privileges((1 << 11) - 1)
	combined := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: all, GrantOptions: authorization.UpdatePrivilege},
		{Grantor: roles.LookupRef("acl_translation_grantor"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: all, GrantOptions: all},
	}}
	value, err = pgcatalog.ClassACL(e.catalog.owner(), roles, array, combined)
	if err != nil {
		t.Fatal(err)
	}
	if value.ArrayLen() != 2 {
		t.Fatal("stored order/cardinality", value.ArrayLen())
	}
	for i, want := range []struct{ grantee, grantor, options uint32 }{{10, 10, 4}, {0, 100001, 17407}} {
		authority, grantee, grantor, mask, options, err := value.ArrayElement(i).ACLItem()
		if err != nil || authority != &e.roles.aclAuthority || grantee != want.grantee || grantor != want.grantor || mask != 17407 || options != want.options {
			t.Fatal("combined masks/order/principals", i, grantee, grantor, mask, options, err)
		}
	}
}
