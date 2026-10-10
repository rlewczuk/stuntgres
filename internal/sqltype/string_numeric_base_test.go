package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func stringNumericBaseTypes() map[ID]*Descriptor {
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	return types
}

func TestStringNumericBaseProviders(t *testing.T) {
	types := stringNumericBaseTypes()
	text := types[Text].Usage()
	for _, tc := range []struct {
		id            ID
		n             int64
		bin, oct, hex string
	}{
		{Int4, 0, "0", "0", "0"},
		{Int8, 42, "101010", "52", "2a"},
		{Int4, -1, "11111111111111111111111111111111", "37777777777", "ffffffff"},
		{Int8, -1, "1111111111111111111111111111111111111111111111111111111111111111", "1777777777777777777777", "ffffffffffffffff"},
		{Int4, -2147483648, "10000000000000000000000000000000", "20000000000", "80000000"},
		{Int8, -9223372036854775808, "1000000000000000000000000000000000000000000000000000000000000000", "1000000000000000000000", "8000000000000000"},
	} {
		for i, name := range []string{"to_bin", "to_oct", "to_hex"} {
			p, err := BindString(name, text, text, []Usage{types[tc.id].Usage()}, [4]bool{}, false)
			if err != nil {
				t.Fatal(err)
			}
			v, err := Integer(types[tc.id], tc.n)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Evaluate(context.Background(), [4]Value{v}, InputEnvironment{}, OutputEnvironment{})
			want := []string{tc.bin, tc.oct, tc.hex}[i]
			if err != nil || got.Type() != text.Type || got.text != want || p.ArgumentUsage(0) != types[tc.id].Usage() {
				t.Fatal(name, got, want, err)
			}
			got, err = p.Evaluate(context.Background(), [4]Value{Null(types[tc.id])}, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != text.Type {
				t.Fatal(got, err)
			}
		}
	}
	for _, tc := range []struct{ raw, digest string }{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"abc", "900150983cd24fb0d6963f7d28e17f72"},
		{"é", "66ddcd97cfdeabb2f6fb8a999b4bc76f"},
	} {
		p, err := BindString("md5", text, text, []Usage{text}, [4]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(context.Background(), [4]Value{String(text.Type, tc.raw)}, InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != text.Type || got.text != tc.digest {
			t.Fatal(got, err)
		}
	}
}

func TestStringNumericBaseOwnershipAndSignatures(t *testing.T) {
	types, foreign := stringNumericBaseTypes(), stringNumericBaseTypes()
	text := types[Text].Usage()
	for _, name := range []string{"to_bin", "to_oct", "to_hex", "md5"} {
		id, textual := Int4, [4]bool{}
		if name == "md5" {
			id, textual = Text, [4]bool{true}
		}
		u := types[id].Usage()
		p, err := BindString(name, text, text, []Usage{u}, textual, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]Usage{nil, {u, u}, {types[Bytea].Usage()}, {Usage{}}} {
			if _, err := BindString(name, text, text, args, textual, false); err == nil {
				t.Fatal("invalid signature accepted", name, args)
			}
		}
		if _, err := BindString(name, foreign[Text].Usage(), text, []Usage{u}, textual, false); err == nil {
			t.Fatal("foreign result accepted")
		}
		if _, err := p.Evaluate(context.Background(), [4]Value{Null(foreign[id])}, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("foreign null accepted")
		}
		got, err := p.Evaluate(context.Background(), [4]Value{Null(types[id])}, InputEnvironment{}, OutputEnvironment{})
		if err != nil || !got.IsNull() || got.Type() != text.Type {
			t.Fatal(got, err)
		}
	}
	if _, err := BindString("to_hex", text, text, []Usage{types[Int2].Usage()}, [4]bool{}, false); err == nil {
		t.Fatal("SMALLINT formal accepted")
	}
}

func TestStringNumericBaseMD5Cancellation(t *testing.T) {
	types := stringNumericBaseTypes()
	text := types[Text].Usage()
	p, err := BindString("md5", text, text, []Usage{text}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	input := String(text.Type, strings.Repeat("abcd", 8192))
	for _, checkpoint := range []int{4, 5, 6} {
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
		got, err := p.Evaluate(ctx, [4]Value{input}, InputEnvironment{}, OutputEnvironment{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal(checkpoint, got, err)
		}
	}
}
