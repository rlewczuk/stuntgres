package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func attrUnitOwnDomainNullability(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := attrUnitState(t, f)
	view := f.e.Catalog.Read(state)
	parent, _ := view.LookupType(49200)
	child, err := sqltype.Domain(49202, "child", "public.child", parent.Usage(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !child.DomainNotNull() || child.OwnDomainNotNull() {
		t.Fatal("fixture must distinguish inherited and own not-null")
	}
	descriptor := &catalog.Type{SQLType: child, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
	state.SetDescriptor(descriptor.Address(), descriptor)
	state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	relation := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
	relation.Columns = []catalog.Column{{Name: "child", Usage: child.Usage(), Nullable: true, NullabilityKnown: true}}
	relation.Attributes = []int32{5}
	relation.ColumnMeta = catalog.ColumnMetadata{HighWater: 5}
	typ, err := sqltype.Composite(49000, "pair", "public.pair", 0, []sqltype.CompositeMember{{Attribute: 5, Name: "child", Usage: child.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	owner := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 49000})
	owner.SQLType = typ
	state.SetDescriptor(owner.Address(), &owner)
	state.SetDescriptor(relation.Address(), &relation)
	in := attrUnitInput(t, f, f.e.Catalog.Read(state), f.owner)
	rows := attrUnitRows(t, in, 1)
	row := rows[0]
	text := map[int]string{0: "captured db", 1: "public", 2: "pair", 3: "child", 6: "YES", 7: "USER-DEFINED", 22: "captured db", 23: "public", 24: "child", 29: "5", 30: "NO"}
	for i, value := range row {
		if i == 4 {
			base, err := value.BaseValue()
			n, integerErr := base.Integer()
			if err != nil || integerErr != nil || n != 5 {
				t.Fatal("physical ordinal", value, err, integerErr)
			}
		} else if want, ok := text[i]; ok {
			base, err := value.BaseValue()
			got, textErr := base.Text()
			if err != nil || textErr != nil || got != want {
				t.Fatal("nested domain cell", i, got, want, err, textErr)
			}
		} else if !value.IsNull() {
			t.Fatal("immediate domain base must not recursively invent dimensions/defaults", i, value)
		}
	}
}

func attrUnitDroppedMetadata(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := attrUnitState(t, f)
	view := f.e.Catalog.Read(state)
	relation := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
	relation.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{3: {Name: "label", Number: 3}}
	state.SetDescriptor(relation.Address(), &relation)
	rows := attrUnitRows(t, attrUnitInput(t, f, f.e.Catalog.Read(state), f.owner), 2)
	for _, row := range rows {
		if infoUserMappingsUnitText(t, row[3]) == "label" || infoUserMappingsUnitText(t, row[29]) == "3" {
			t.Fatal("dropped attribute contributed a row")
		}
	}
}
