package sqltype

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestFTSArrayConfigTypedAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	ctx := context.Background()
	args := [3]Usage{types[1009].Usage()}
	p, err := BindFTSVector(FTSArrayToVector, types[TSVector].Usage(), args)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{{"{b,a,a}", "'a' 'b'"}, {"[0:1][4:5]={{b,a},{a,c}}", "'a' 'b' 'c'"}, {"{}", ""}} {
		v, err := types[1009].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		before := v.array
		out, err := p.Evaluate(ctx, [3]Value{v})
		if err != nil || out.Type() != types[TSVector] || out.IsNull() {
			t.Fatal("constructor authority", err)
		}
		text, err := out.Type().Output(ctx, out)
		if err != nil || text != tc.want || v.array != before {
			t.Fatal(text, err)
		}
		input, err := types[1009].Output(ctx, v)
		if err != nil || input != tc.input {
			t.Fatal("source shape/value changed", input, err)
		}
	}
	out, err := p.Evaluate(ctx, [3]Value{Null(types[1009])})
	if err != nil || !out.IsNull() || out.Type() != types[TSVector] {
		t.Fatal("typed whole NULL", err)
	}
	for _, bad := range []Value{
		Null(foreign[1009]),
		{typ: types[1009]},
		{typ: types[1009], array: &arrayPayload{dimensions: []ArrayDimension{{Lower: 1, Length: 1}}}},
		{typ: types[1009], array: &arrayPayload{dimensions: []ArrayDimension{{Lower: 1, Length: 1}}, elements: []Value{String(foreign[Text], "a")}}},
	} {
		if _, err := p.Evaluate(ctx, [3]Value{bad}); err == nil {
			t.Fatal("invalid array authority/payload admitted", bad)
		}
	}
	forged := p
	forged.inputs[0] = types[Text]
	if _, err := forged.Evaluate(ctx, [3]Value{String(types[Text], "a")}); err == nil {
		t.Fatal("forged non-array plan admitted")
	}
	if _, err := BindFTSVector(FTSArrayToVector, foreign[TSVector].Usage(), args); err == nil {
		t.Fatal("foreign result")
	}
	for _, bad := range []Usage{foreign[1009].Usage(), {Type: types[1009], Typmod: 0, Collation: args[0].Collation}, {Type: types[1009], Typmod: -1}, {Type: types[1009], Typmod: -1, Collation: CollationRef{ID: 88101, Derivation: Default}}} {
		if _, err := BindFTSVector(FTSArrayToVector, types[TSVector].Usage(), [3]Usage{bad}); err == nil {
			t.Fatal("foreign/invalid usage admitted", bad)
		}
	}
	for _, collation := range []CollationRef{{ID: 950, Derivation: Default}, {ID: 951, Derivation: Default}} {
		u := args
		u[0].Collation = collation
		plan, err := BindFTSVector(FTSArrayToVector, types[TSVector].Usage(), u)
		if err != nil {
			t.Fatal("valid array collation rejected", err)
		}
		input, err := types[1009].Input(ctx, "{é,a,Z,a}", -1)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Evaluate(ctx, [3]Value{input})
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Type().Output(ctx, value)
		if err != nil || text != "'Z' 'a' 'é'" {
			t.Fatal("collation changed byte-exact construction", text, err)
		}
	}
	for _, damage := range []string{"element", "text", "vector"} {
		array := *types[1009]
		switch damage {
		case "element":
			array.element = foreign[Text]
		case "text":
			array.ftsText = foreign[Text]
		case "vector":
			array.ftsVector = foreign[TSVector]
		}
		if _, err := BindFTSVector(FTSArrayToVector, types[TSVector].Usage(), [3]Usage{array.Usage()}); err == nil {
			t.Fatal("forged companion authority", damage)
		}
	}
	// Fresh template clones must reconnect the constructor's bidirectional
	// TEXT[]/TSVECTOR/TEXT links entirely within the new descriptor generation.
	for _, generation := range []map[ID]*Descriptor{types, foreign} {
		if generation[1009].ftsVector != generation[TSVector] || generation[1009].ftsText != generation[Text] || generation[TSVector].ftsTextArray != generation[1009] {
			t.Fatal("constructor companions escaped builtin generation")
		}
	}
}

func TestFTSArrayConfigTypedCancellation(t *testing.T) {
	types := testTypes()
	values := make([]Value, 2048)
	for i := range values {
		values[i] = String(types[Text], fmt.Sprintf("w%04d", len(values)-i))
	}
	source, err := ArrayValue(types[1009], []ArrayDimension{{Lower: -2, Length: int32(len(values))}}, values)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]Value{}, source.array.elements...)
	p, err := BindFTSVector(FTSArrayToVector, types[TSVector].Usage(), [3]Usage{types[1009].Usage()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &vectorFunctionCancelContext{Context: context.Background(), remaining: 5}
	out, err := p.Evaluate(ctx, [3]Value{source})
	if out.Type() != nil || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, source.array.elements) {
		t.Fatal("typed interior cancellation/ownership", out, err)
	}
	out, err = p.Evaluate(context.Background(), [3]Value{source})
	if err != nil || out.IsNull() || out.ftsVector.LexemeCount() != len(values) {
		t.Fatal("typed recovery", err)
	}
}
