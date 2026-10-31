package types_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

// Exercise the scalar transport seam, not an excluded ACL index provider.
func TestDomainCheckIndexSnapshotACLReownership(t *testing.T) {
	source, target := engine.New(), engine.New()
	aclUnitRoles(source, "snapshot_reader", "snapshot_grantor")
	aclUnitRoles(target, "snapshot_spacer", "snapshot_reader", "snapshot_grantor")
	value := aclUnitValue(t, source, "snapshot_reader=r*/snapshot_grantor")
	fromArray := aclUnitType(t, source, sqltype.ACLItemArray)
	toArray := aclUnitType(t, target, sqltype.ACLItemArray)
	array, err := sqltype.OwnedArrayValue(fromArray, []sqltype.ArrayDimension{{Lower: -1, Length: 2}}, []sqltype.Value{value, sqltype.Null(value.Type())})
	if err != nil {
		t.Fatal(err)
	}
	fromDomain, err := sqltype.Domain(45000, "snapshot_acl", "snapshot_acl", fromArray.Usage())
	if err != nil {
		t.Fatal(err)
	}
	toDomain, err := sqltype.Domain(45000, "snapshot_acl", "snapshot_acl", toArray.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := fromDomain.Wrap(array)
	if err != nil {
		t.Fatal(err)
	}
	fromComposite, err := sqltype.Composite(45001, "snapshot_record", "snapshot_record", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "nested", Usage: fromDomain.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	toComposite, err := sqltype.Composite(45001, "snapshot_record", "snapshot_record", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "nested", Usage: toDomain.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.OwnedCompositeValue(fromComposite, []sqltype.Value{wrapped})
	if err != nil {
		t.Fatal(err)
	}
	importer := target.RoleStore.ACLItemImporter(source.RoleStore, &target.Catalog, source.Roles(), target.Roles(), nil)
	snapshot := &index.Snapshot{}
	mapped, err := snapshot.Datum(composite, toComposite, nil, importer)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := mapped.CompositeField(0).BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	authority, grantee, grantor, privileges, options, err := nested.ArrayElement(0).ACLItem()
	reader, _ := target.Roles().Lookup("snapshot_reader")
	writer, _ := target.Roles().Lookup("snapshot_grantor")
	if err != nil || authority != &target.RoleStore.ACLAuthority || grantee != uint32(reader.Ref.ID()) || grantor != uint32(writer.Ref.ID()) || privileges != 2 || options != 2 {
		t.Fatal("snapshot lost ACL principal authority/options", err)
	}
	if mapped.Type() != toComposite || mapped.CompositeField(0).Type() != toDomain || nested.Type() != toArray || !nested.ArrayElement(1).IsNull() {
		t.Fatal("snapshot lost recursive target generations or NULL")
	}
	if array.ArrayElement(0) != value || composite.CompositeField(0).Type() != fromDomain {
		t.Fatal("snapshot mutated source payload")
	}
	again, err := snapshot.Datum(mapped, toComposite, nil, importer)
	if err != nil || again != mapped {
		t.Fatal("destination-owned ACL copied/reimported", err)
	}
	if source.Catalog.State.ACLItemImport() != nil || target.Catalog.State.ACLItemImport() != nil {
		t.Fatal("snapshot published import callback")
	}
}
