package sqltype

import (
	"context"
	"testing"
)

func TestCastImplementedImmutability(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	input := InputEnvironment{}
	output := OutputEnvironment{}
	one := castUnitValue(t, types[Int4], "1", input)
	zero := castUnitValue(t, types[Int4], "0", input)
	dims := []ArrayDimension{{Lower: 0, Length: 3}}
	elements := []Value{one, Null(types[Int4]), zero}
	source, err := ArrayValue(types[1007], dims, elements)
	if err != nil {
		t.Fatal(err)
	}
	dims[0].Lower = 99
	elements[0] = zero
	castUnitOutput(t, source, output, "[0:2]={1,NULL,0}")
	identity, err := PlanCoercion(types[1007].Usage(), types[1007].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	same, err := identity.Apply(ctx, source, input, output)
	if err != nil || same.array != source.array || same.Type() != source.Type() {
		t.Fatal("identity copied immutable payload", same, err)
	}
	conversion, err := PlanCoercion(types[1007].Usage(), types[1000].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := conversion.Apply(ctx, source, input, output)
	if err != nil {
		t.Fatal(err)
	}
	castUnitOutput(t, changed, output, "[0:2]={t,NULL,f}")
	if changed.array == source.array || changed.ArrayDimension(0) != (ArrayDimension{Lower: 0, Length: 3}) {
		t.Fatal("changed array lost independent payload/bounds")
	}
	for _, v := range changed.array.elements {
		if v.Type() != types[Bool] {
			t.Fatal("mapped leaf retained foreign source type", v.Type())
		}
	}
	castUnitOutput(t, source, output, "[0:2]={1,NULL,0}")
	empty := castUnitValue(t, types[1007], "{}", input)
	widening, err := PlanCoercion(types[1007].Usage(), types[1016].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := widening.Apply(ctx, empty, input, output)
	if err != nil || mapped.Type() != types[1016] || mapped.array != empty.array {
		t.Fatal("empty mapping copied an unchanged payload", mapped, err)
	}
	castUnitOutput(t, mapped, output, "{}")
	text := castUnitValue(t, types[1009], "[0:2]={00000000000000000000000000000001,NULL,bad}", input)
	original := text.array
	uuid, err := PlanCoercion(types[1009].Usage(), types[2951].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := uuid.Apply(ctx, text, input, output)
	castUnitError(t, err, "22P02", `invalid input syntax for type uuid: "bad"`)
	if failed.Type() != nil || text.array != original {
		t.Fatal("failed map published partial result or replaced source")
	}
	castUnitOutput(t, text, output, "[0:2]={00000000000000000000000000000001,NULL,bad}")
	for _, v := range text.array.elements {
		if v.Type() != types[Text] {
			t.Fatal("failed conversion retagged source member")
		}
	}
	members := []CompositeMember{{Attribute: 1, Name: "n", Usage: types[Int4].Usage()}, {Attribute: 3, Name: "s", Usage: types[Text].Usage()}}
	record, err := RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	fields := []Value{one, Null(types[Text])}
	tuple, err := CompositeValue(record, fields)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = zero
	tupleIdentity, err := PlanCoercion(record.Usage(), record.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	tupleAgain, err := tupleIdentity.Apply(ctx, tuple, input, output)
	if err != nil || tupleAgain.composite != tuple.composite {
		t.Fatal("record identity copied/retagged shape", err)
	}
	castUnitOutput(t, tupleAgain, output, "(1,)")
	if record.CompositeMember(1).Attribute != 3 || tupleAgain.CompositeField(1).Type() != types[Text] || !tupleAgain.CompositeField(1).IsNull() {
		t.Fatal("member identity or typed NULL lost")
	}
}
