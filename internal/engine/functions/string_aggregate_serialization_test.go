package functions

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestStringAggregateSerialization(t *testing.T) {
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Text, sqltype.Bytea} {
		typ, _ := aggregateUnitType(id)
		p := NewStringAggregateProvider(typ.Usage())
		value := func(s string) sqltype.Value {
			if id == sqltype.Bytea {
				v, err := sqltype.Binary(typ, s)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			return sqltype.String(typ, s)
		}
		final := func(s any, want string, null bool) {
			t.Helper()
			v, err := p.Finalize(ctx, s)
			if err != nil || v.IsNull() != null {
				t.Fatalf("final=%v/%v", v, err)
			}
			if null {
				return
			}
			var got string
			if id == sqltype.Bytea {
				got, err = v.Binary()
			} else {
				got, err = v.Text()
			}
			if err != nil || got != want {
				t.Fatalf("final=%q/%v want %q", got, err, want)
			}
		}
		literal := []byte{0, 0, 0, 1, ',', 'a', ';', 'b'}
		decoded, err := p.Deserialize(ctx, literal, true)
		if err != nil {
			t.Fatal(err)
		}
		final(decoded, "a;b", false)
		literal[5] = 'z'
		final(decoded, "a;b", false)
		if id == sqltype.Bytea {
			binaryState, err := p.Deserialize(ctx, []byte{0, 0, 0, 1, ',', 0, 0x80, 0xff}, true)
			if err != nil {
				t.Fatal(err)
			}
			final(binaryState, "\x00\x80\xff", false)
		}
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]string{{"a", ","}, {"b", ";"}} {
			if err := p.Advance(ctx, state, []sqltype.Value{value(pair[0]), value(pair[1])}, nil); err != nil {
				t.Fatal(err)
			}
		}
		encoded, present, err := p.Serialize(ctx, state)
		if err != nil || !present || !bytes.Equal(encoded, []byte{0, 0, 0, 1, ',', 'a', ';', 'b'}) {
			t.Fatalf("literal codec=%x/%v/%v", encoded, present, err)
		}
		roundtrip, err := p.Deserialize(ctx, encoded, present)
		if err != nil {
			t.Fatal(err)
		}
		final(roundtrip, "a;b", false)
		empty, _ := p.Init(ctx)
		data, present, err := p.Serialize(ctx, empty)
		if err != nil || present || len(data) != 0 {
			t.Fatal(data, present, err)
		}
		absent, err := p.Deserialize(ctx, data, present)
		if err != nil {
			t.Fatal(err)
		}
		final(absent, "", true)
		if err := p.Advance(ctx, empty, []sqltype.Value{value(""), value("é")}, nil); err != nil {
			t.Fatal(err)
		}
		data, present, err = p.Serialize(ctx, empty)
		if err != nil || !present || !bytes.Equal(data, []byte{0, 0, 0, 2, 0xc3, 0xa9}) {
			t.Fatal(data, present, err)
		}
		final(empty, "", false)
		if err := p.Advance(ctx, empty, []sqltype.Value{value("b"), sqltype.Null(typ)}, nil); err != nil {
			t.Fatal(err)
		}
		final(empty, "b", false)
		leader, _ := p.Init(ctx)
		if err := p.Combine(ctx, leader, state, nil); err != nil {
			t.Fatal(err)
		}
		if err := p.Advance(ctx, state, []sqltype.Value{value("mutated"), value("!")}, nil); err != nil {
			t.Fatal(err)
		}
		final(leader, "a;b", false)
		if err := p.Combine(ctx, leader, empty, nil); err != nil {
			t.Fatal(err)
		}
		final(leader, "a;béb", false)
		for _, bad := range [][]byte{{}, {0, 0, 0}, {0, 0, 0, 2, 'x'}, {255, 255, 255, 255}} {
			if _, err := p.Deserialize(ctx, bad, true); err == nil {
				t.Fatalf("accepted malformed %x", bad)
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := p.Serialize(canceled, state); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := p.Deserialize(canceled, encoded, true); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := p.Combine(canceled, leader, state, nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
