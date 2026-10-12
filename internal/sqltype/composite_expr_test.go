package sqltype

import (
	"fmt"
	"testing"
)

func TestCompositeExprRecordShape(t *testing.T) {
	b := compositeBuiltins()
	members := []CompositeMember{{1, "same", b[Int4].Usage()}, {7, "same", b[Text].Usage()}}
	a, err := RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	c, err := RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	if a == c || a.ID() != 2249 || c.ID() != 2249 || a.AssociatedArray() != 2287 || a.DatabaseName() != "RECORD" || !a.Concrete() {
		t.Fatal("anonymous occurrence identity or metadata")
	}
	members[0].Name = "mutated"
	if a.CompositeMember(0).Name != "same" {
		t.Fatal("record shape borrowed its definition")
	}
	if index, ok := a.CompositeAttributeIndex(7); !ok || index != 1 {
		t.Fatal("stable attribute projection", index, ok)
	}
	if _, ok := a.CompositeAttributeIndex(2); ok {
		t.Fatal("attribute gap became a live member")
	}
	nested, err := RecordShape([]CompositeMember{{1, "left", a.Usage()}, {2, "right", c.Usage()}})
	if err != nil || nested.CompositeLen() != 2 {
		t.Fatal("distinct nested RECORD occurrences", err)
	}
	foreign := compositeBuiltins()
	other, err := RecordShape([]CompositeMember{{1, "foreign", foreign[Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordShape([]CompositeMember{{1, "local", a.Usage()}, {2, "foreign", other.Usage()}}); err == nil {
		t.Fatal("foreign nested member ownership admitted")
	}
	limit := make([]CompositeMember, 1664)
	for i := range limit {
		limit[i] = CompositeMember{int32(i + 1), fmt.Sprintf("f%d", i+1), b[Int4].Usage()}
	}
	if _, err := RecordShape(limit); err != nil {
		t.Fatal("1664-field expression shape", err)
	}
	if _, err := RecordShape(append(limit, CompositeMember{1665, "overflow", b[Int4].Usage()})); err == nil {
		t.Fatal("1665-field expression shape admitted")
	}
	if _, err := Composite(50000, "named", "named", 50001, limit[:1600]); err != nil {
		t.Fatal("1600-field named type", err)
	}
	if _, err := Composite(50000, "named", "named", 50001, limit[:1601]); err == nil {
		t.Fatal("named storage limit widened")
	}
	empty, err := RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := OwnedCompositeValue(empty, nil)
	if err != nil || value.IsNull() || value.CompositeLen() != 0 {
		t.Fatal("empty record is not SQL NULL", err)
	}
	if _, err := OwnedCompositeValue(a, []Value{Null(foreign[Int4]), Null(b[Text])}); err == nil {
		t.Fatal("foreign payload generation admitted")
	}
}
