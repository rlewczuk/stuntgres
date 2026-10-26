package sqltype

import (
	"context"
	"testing"
)

func TestRecordSignatureShapeAuthority(t *testing.T) {
	types := compositeBuiltins()
	for _, id := range []ID{2283, 2277, 2776, 3500, 3831, 4537, 5077, 5078, 5079, 5080, 4538} {
		members := []CompositeMember{{Attribute: 1, Name: "value", Usage: types[id].Usage()}, {Attribute: 2, Name: "label", Usage: types[Text].Usage()}}
		shape, err := RecordSignatureShape(members)
		if err != nil {
			t.Fatal(id, err)
		}
		members[0].Name = "mutated"
		forgedNull := Value{typ: shape, null: true}
		if !shape.IsComposite() || shape.CompositeMember(0).Name != "value" || shape.CompositeMember(0).Usage.Type != types[id] || shape.Concrete() || shape.Usage().Valid() || forgedNull.ValidResult(shape.Usage()) {
			t.Fatal("declaration shape acquired value authority", id)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("declaration shape acquired NULL constructor authority", id)
				}
			}()
			Null(shape)
		}()
		if _, err := shape.Input(context.Background(), "(1,x)", -1); err == nil {
			t.Fatal("declaration shape acquired input codec", id)
		}
		if _, err := shape.Output(context.Background(), forgedNull); err == nil {
			t.Fatal("declaration shape acquired output codec", id)
		}
		if _, err := OwnedCompositeValue(shape, []Value{{typ: types[id], null: true}, Null(types[Text])}); err == nil {
			t.Fatal("declaration shape acquired tuple value", id)
		}
		if err := CheckStoredColumn("value", shape.Usage()); err == nil {
			t.Fatal("declaration shape became storable", id)
		}
		copy := *types[id]
		for _, forged := range []*Descriptor{&copy, LookupOnly(id, types[id].Name(), types[id].AssociatedArray())} {
			if _, err := RecordSignatureShape([]CompositeMember{{Attribute: 1, Name: "forged", Usage: forged.Usage()}}); err == nil {
				t.Fatal("forged polymorphic member admitted", id)
			}
		}
		if _, err := RecordSignatureShape([]CompositeMember{{Attribute: 1, Name: "nested", Usage: shape.Usage()}}); err == nil {
			t.Fatal("unresolved nested tuple admitted", id)
		}
	}
	for _, id := range []ID{2276, 2281, 2280, 3115, 269, 325, 3310, 2249, 2279, 3838, Unknown} {
		if _, err := RecordSignatureShape([]CompositeMember{{Attribute: 1, Name: "invalid", Usage: types[id].Usage()}}); err == nil {
			t.Fatal("non-polymorphic signature-only member admitted", id)
		}
	}
	members := []CompositeMember{{Attribute: 1, Name: "value", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "label", Usage: types[Text].Usage()}}
	concrete, err := RecordSignatureShape(members)
	if err != nil || !concrete.Concrete() || !concrete.Usage().Valid() {
		t.Fatal("concrete declaration tuple lost ordinary authority", err)
	}
	foreign := compositeBuiltins()
	if _, err := RecordSignatureShape([]CompositeMember{{Attribute: 1, Name: "first", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "second", Usage: foreign[Int4].Usage()}}); err == nil {
		t.Fatal("incoherent child generations admitted")
	}
}
