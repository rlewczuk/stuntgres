package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestForeignKeyTriggerAttributes(t *testing.T) {
	address := func(oid ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	}
	child := &Relation{ObjectAddress: address(100), RelationKind: TableRelation, Attributes: []int32{1}}
	parent := &Relation{ObjectAddress: address(101), RelationKind: TableRelation, Attributes: []int32{2}}
	index := &Relation{ObjectAddress: address(102), SQLIndex: &IndexDefinition{Parent: parent.Address(), Unique: true, Ready: true, Live: true, Valid: true, Keys: []IndexKey{{Attribute: 2}}}}
	state := NewState(1)
	for _, d := range []*Relation{child, parent, index} {
		state.SetDescriptor(d.Address(), d)
	}
	read := NewRead(&Owner{Domain: &ids.Domain{}}, state)
	simple := func(_ ids.ObjectAddress, key IndexKey) (int32, bool) { return key.Attribute, key.Derived == nil }
	for _, action := range []struct {
		kind     parser.ForeignKeyAction
		function ids.OID
	}{{parser.ForeignKeyNoAction, 1654}, {parser.ForeignKeyRestrict, 1648}, {parser.ForeignKeyCascade, 1646}, {parser.ForeignKeySetNull, 1650}, {parser.ForeignKeySetDefault, 1652}} {
		constraint := ForeignKeyConstraint{OID: 110, TriggerOIDs: [4]ids.OID{111, 112, 113, 114}, Attributes: []int32{1}, Target: parent.Address(), Referenced: []int32{2}, Index: index.Address(), Deferrable: true, InitiallyDeferred: true, DeleteAction: action.kind, UpdateAction: action.kind}
		copy := *child
		copy.Constraints.ForeignKeys = []ForeignKeyConstraint{constraint}
		state.SetDescriptor(child.Address(), &copy)
		count := 0
		for fact := range ForeignKeyTriggerFacts(read) {
			count++
			attributes, err := fact.Attributes(read, simple)
			if err != nil {
				t.Fatal(err)
			}
			want := ForeignKeyTriggerAttributes{Function: action.function, Flags: 9, Opposite: child.Address(), Deferrable: action.kind == parser.ForeignKeyNoAction, InitiallyDeferred: action.kind == parser.ForeignKeyNoAction}
			switch fact.Slot {
			case 1:
				want.Function++
				want.Flags = 17
			case 2:
				want = ForeignKeyTriggerAttributes{Function: 1644, Flags: 5, Opposite: parent.Address(), Deferrable: true, InitiallyDeferred: true}
			case 3:
				want = ForeignKeyTriggerAttributes{Function: 1645, Flags: 17, Opposite: parent.Address(), Deferrable: true, InitiallyDeferred: true}
			}
			if attributes != want {
				t.Fatalf("action %v slot %d: %+v; want %+v", action.kind, fact.Slot, attributes, want)
			}
			bad := fact
			bad.OID++
			if _, err := bad.Attributes(read, simple); err == nil {
				t.Fatal("accepted foreign trigger identity")
			}
			bad = fact
			bad.Relation = index.Address()
			if _, err := bad.Attributes(read, simple); err == nil {
				t.Fatal("accepted wrong event relation")
			}
			if _, err := fact.Attributes(read, func(ids.ObjectAddress, IndexKey) (int32, bool) { return 0, false }); err == nil {
				t.Fatal("accepted unproven index authority")
			}
		}
		if count != 4 {
			t.Fatal("incomplete RI inventory", count)
		}
	}
}
