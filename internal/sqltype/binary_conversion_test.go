package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestBinaryConversionSignatures(t *testing.T) {
	types := map[ID]*Descriptor{}
	other := map[ID]*Descriptor{}
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	for _, d := range Builtins() {
		other[d.ID()] = d
	}
	for _, tc := range []struct {
		name      string
		ids       []ID
		result    ID
		raw       []string
		want      string
		immutable bool
	}{
		{"convert", []ID{Bytea, Name, Name}, Bytea, []string{"\xe9", "LATIN1", "UTF8"}, "é", false},
		{"convert_from", []ID{Bytea, Name}, Text, []string{"\xe9", "LATIN1"}, "é", false},
		{"convert_to", []ID{Text, Name}, Bytea, []string{"é", "LATIN1"}, "\xe9", false},
		{"encode", []ID{Bytea, Text}, Text, []string{"\x00\xff", "hex"}, "00ff", true},
		{"decode", []ID{Text, Text}, Bytea, []string{"00ff", "hex"}, "\x00\xff", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usages := make([]Usage, len(tc.ids))
			values := make([]Value, len(tc.ids))
			for i, id := range tc.ids {
				usages[i] = types[id].Usage()
				if id == Bytea {
					values[i], _ = Binary(types[id], tc.raw[i])
				} else {
					values[i] = String(types[id], tc.raw[i])
				}
			}
			plan, err := BindBinary(tc.name, types[tc.result].Usage(), usages...)
			if err != nil || plan.Immutable() != tc.immutable {
				t.Fatal(plan, err)
			}
			got, err := plan.Evaluate(context.Background(), values...)
			if err != nil || got.text != tc.want || got.Type() != types[tc.result] {
				t.Fatal(got, err)
			}
			for i := range values {
				saved := values[i]
				values[i] = Null(types[tc.ids[i]])
				got, err = plan.Evaluate(context.Background(), values...)
				if err != nil || !got.IsNull() || got.Type() != types[tc.result] {
					t.Fatal(got, err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				got, err = plan.Evaluate(ctx, values...)
				if !errors.Is(err, context.Canceled) || got != (Value{}) {
					t.Fatal(got, err)
				}
				values[i] = Null(other[tc.ids[i]])
				if _, err = plan.Evaluate(context.Background(), values...); err == nil {
					t.Fatal("foreign value accepted")
				}
				values[i] = saved
				savedUsage := usages[i]
				usages[i].Typmod = 3
				if _, err = BindBinary(tc.name, types[tc.result].Usage(), usages...); err == nil {
					t.Fatal("typmod accepted")
				}
				usages[i] = savedUsage
			}
			if _, err = BindBinary(tc.name, types[tc.result].Usage(), usages[:len(usages)-1]...); err == nil {
				t.Fatal("arity accepted")
			}
			if _, err = BindBinary(tc.name, types[Int4].Usage(), usages...); err == nil {
				t.Fatal("result ID accepted")
			}
			for i, id := range tc.ids {
				if id == tc.result {
					if _, err = BindBinary(tc.name, other[tc.result].Usage(), usages...); err == nil {
						t.Fatal("foreign same-ID result accepted", i)
					}
				}
			}
			if tc.name == "decode" {
				usages[1] = other[Text].Usage()
				if _, err = BindBinary(tc.name, types[Bytea].Usage(), usages...); err == nil {
					t.Fatal("mixed TEXT owners accepted")
				}
			}
		})
	}
}
