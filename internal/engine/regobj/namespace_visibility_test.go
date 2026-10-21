package regobj

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestVisibilityInquiryCollationSelection(t *testing.T) {
	address := func(id ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: id}
	}
	for _, tc := range []struct {
		name       string
		candidates []CollationCandidate
		want       ids.ObjectAddress
	}{
		{"exact-first", []CollationCandidate{{Address: address(1), Encoding: 6}, {Address: address(2), Encoding: -1}, {Address: address(3), Encoding: 8}}, address(1)},
		{"fallback-first", []CollationCandidate{{Address: address(2), Encoding: -1}, {Address: address(3), Encoding: 8}, {Address: address(1), Encoding: 6}}, address(1)},
		{"fallback-only", []CollationCandidate{{Address: address(2), Encoding: -1}}, address(2)},
		{"wrong-only", []CollationCandidate{{Address: address(3), Encoding: 8}}, ids.ObjectAddress{}},
		{"wrong-and-fallback", []CollationCandidate{{Address: address(3), Encoding: 8}, {Address: address(2), Encoding: -1}}, address(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectCollation(tc.candidates, 6); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	// Exercise the actual namespace-first inquiry path with independently built
	// name candidates, without admitting unsupported native collation DDL.
	a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20001}
	b := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20002}
	first := &catalog.Collation{OID: 1, ObjectName: "same", Encoding: -1}
	second := &catalog.Collation{OID: 2, ObjectName: "same", Encoding: 6}
	state := catalog.NewState(0)
	state.SetDescriptor(first.Address(), first)
	state.SetDescriptor(second.Address(), second)
	state.BindName(ids.NameKey{ClassID: ids.CollationClassID, Namespace: a, Name: "same"}, first.Address())
	state.BindName(ids.NameKey{ClassID: ids.CollationClassID, Namespace: b, Name: "same"}, second.Address())
	view := catalog.NewRead(nil, state)
	if !VisibleCollation(view, []ids.ObjectAddress{a, b}, first) || VisibleCollation(view, []ids.ObjectAddress{a, b}, second) {
		t.Fatal("later exact beat earlier fallback")
	}
	first.Encoding = 8
	if VisibleCollation(view, []ids.ObjectAddress{a, b}, first) || !VisibleCollation(view, []ids.ObjectAddress{a, b}, second) {
		t.Fatal("wrong encoding shadowed later usable candidate")
	}
	if VisibleCollation(view, []ids.ObjectAddress{a}, first) {
		t.Fatal("ineligible subject became visible")
	}
}
