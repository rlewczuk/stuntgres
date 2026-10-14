package sqltype

import (
	"errors"
	"testing"
)

func TestCompositeArrayEvolution(t *testing.T) {
	b := compositeBuiltins()
	old := compositeForTest(t, 50000, CompositeMember{2, "id", b[Int4].Usage()}, CompositeMember{7, "note", b[Text].Usage()})
	array, err := Array(50001, "_pair", old)
	if err != nil {
		t.Fatal(err)
	}
	input := `[0:2]={"(7,x)",NULL,"(9,y)"}`
	value := compositeInputForTest(t, array, input)
	same, err := value.Retag(array)
	if err != nil || same.array != value.array {
		t.Fatal("unchanged array copied", err)
	}
	next := compositeForTest(t, 50000, CompositeMember{2, "id", b[Int4].Usage()}, CompositeMember{9, "note", b[Text].Usage()})
	target, err := Array(50001, "_pair", next)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := value.Retag(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := compositeOutputForTest(t, mapped); got != `[0:2]={"(7,)",NULL,"(9,)"}` {
		t.Fatal(got)
	}
	if compositeOutputForTest(t, value) != input || !mapped.ArrayElement(1).IsNull() || mapped.ArrayElement(1).Type() != next {
		t.Fatal("source mutation or foreign NULL")
	}
	failure := errors.New("child")
	if _, err := value.MapArray(target, func(Value, *Descriptor) (Value, error) { return Value{}, failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := value.MapArray(target, func(Value, *Descriptor) (Value, error) { return Null(old), nil }); err == nil {
		t.Fatal("foreign element generation admitted")
	}
}

func TestCompositeArrayCodec(t *testing.T) {
	b := compositeBuiltins()
	row := compositeForTest(t, 50000, CompositeMember{2, "id", b[Int4].Usage()}, CompositeMember{7, "note", b[Text].Usage()})
	array, err := Array(50001, "_pair", row)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`{}`, `{{"(7,x)",NULL},{"(9,y)","(,)"}}`, `{"(7,\"a,b\")","(8,\"\")","(9,\"a\\\\b\")"}`} {
		value := compositeInputForTest(t, array, text)
		if got := compositeOutputForTest(t, value); got != text {
			t.Fatalf("got %q want %q", got, text)
		}
	}
	empty := compositeForTest(t, 50002)
	emptyArray, err := Array(50003, "_empty", empty)
	if err != nil {
		t.Fatal(err)
	}
	if got := compositeOutputForTest(t, compositeInputForTest(t, emptyArray, `{(),NULL}`)); got != `{(),NULL}` {
		t.Fatal(got)
	}
}
