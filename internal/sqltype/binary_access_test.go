package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func binaryAccessPlan(t *testing.T, name string) (BinaryPlan, []Value) {
	t.Helper()
	types := map[ID]*Descriptor{}
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	ids, result := []ID{Bytea}, Int8
	switch name {
	case "get_bit":
		ids, result = []ID{Bytea, Int8}, Int4
	case "get_byte":
		ids, result = []ID{Bytea, Int4}, Int4
	case "set_bit":
		ids, result = []ID{Bytea, Int8, Int4}, Bytea
	case "set_byte":
		ids, result = []ID{Bytea, Int4, Int4}, Bytea
	}
	usages := make([]Usage, len(ids))
	values := make([]Value, len(ids))
	for i, id := range ids {
		usages[i] = types[id].Usage()
		if i == 0 {
			values[i], _ = Binary(types[id], "\x01\x80")
		} else {
			values[i], _ = Integer(types[id], 0)
		}
	}
	p, err := BindBinary(name, types[result].Usage(), usages...)
	if err != nil {
		t.Fatal(err)
	}
	if p.ResultUsage().Type.ID() != result || !p.Immutable() {
		t.Fatal(p)
	}
	return p, values
}

func TestBinaryAccessValues(t *testing.T) {
	for _, tc := range []struct {
		name             string
		offset, newvalue int64
		number           int64
		raw              string
	}{
		{"bit_count", 0, 0, 2, ""}, {"get_bit", 0, 0, 1, ""}, {"get_bit", 7, 0, 0, ""}, {"get_bit", 8, 0, 0, ""}, {"get_bit", 15, 0, 1, ""},
		{"get_byte", 0, 0, 1, ""}, {"get_byte", 1, 0, 128, ""}, {"set_bit", 7, 1, 0, "\x81\x80"}, {"set_bit", 15, 0, 0, "\x01\x00"},
		{"set_byte", 0, -257, 0, "\xff\x80"}, {"set_byte", 0, 256, 0, "\x00\x80"}, {"set_byte", 0, 257, 0, "\x01\x80"},
	} {
		p, v := binaryAccessPlan(t, tc.name)
		original := v[0]
		if len(v) > 1 {
			v[1], _ = Integer(v[1].typ, tc.offset)
		}
		if len(v) > 2 {
			v[2], _ = Integer(v[2].typ, tc.newvalue)
		}
		got, err := p.Evaluate(context.Background(), v...)
		if err != nil {
			t.Fatal(tc, err)
		}
		if got.typ.ID() == Bytea {
			if got.text != tc.raw {
				t.Fatal(tc, got)
			}
		} else if got.integer != tc.number {
			t.Fatal(tc, got)
		}
		if v[0] != original {
			t.Fatal("source mutated")
		}
	}
}

func TestBinaryAccessValidation(t *testing.T) {
	for _, name := range []string{"bit_count", "get_bit", "get_byte", "set_bit", "set_byte"} {
		t.Run(name, func(t *testing.T) {
			p, v := binaryAccessPlan(t, name)
			usages := make([]Usage, len(v))
			for i := range v {
				usages[i] = v[i].typ.Usage()
			}
			if _, err := BindBinary(name, p.result, usages[:len(usages)-1]...); err == nil {
				t.Fatal("arity")
			}
			bad := p.result
			bad.Typmod = 2
			if _, err := BindBinary(name, bad, usages...); err == nil {
				t.Fatal("result typmod")
			}
			for i := range usages {
				badArgs := append([]Usage(nil), usages...)
				badArgs[i].Typmod = 2
				if _, err := BindBinary(name, p.result, badArgs...); err == nil {
					t.Fatal("argument typmod")
				}
			}
			foreign := byteaDescriptor(t)
			foreignValue, _ := Binary(foreign, "\x01\x80")
			frame := append([]Value(nil), v...)
			frame[0] = foreignValue
			if _, err := p.Evaluate(context.Background(), frame...); err == nil {
				t.Fatal("foreign argument owner")
			}
			if p.result.Type.ID() == Bytea {
				if _, err := BindBinary(name, foreign.Usage(), usages...); err == nil {
					t.Fatal("foreign result owner")
				}
			}
			wrong := p.result
			wrong.Type = v[0].typ
			if p.result.Type.ID() != Bytea {
				if _, err := BindBinary(name, wrong, usages...); err == nil {
					t.Fatal("wrong result ID")
				}
			}
			for i := range v {
				frame := append([]Value(nil), v...)
				frame[i] = Null(frame[i].typ)
				got, err := p.Evaluate(context.Background(), frame...)
				if err != nil || !got.null || got.typ != p.result.Type {
					t.Fatal("strict NULL", got, err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				got, err = p.Evaluate(ctx, frame...)
				if !errors.Is(err, context.Canceled) || got != (Value{}) {
					t.Fatal("context before NULL", got, err)
				}
				frame[i] = Value{}
				if _, err := p.Evaluate(context.Background(), frame...); err == nil {
					t.Fatal("invalid value")
				}
			}
			if len(v) > 1 {
				for _, offset := range []int64{-1, math.MaxInt64, math.MinInt64} {
					if v[1].typ.ID() == Int4 && (offset > math.MaxInt32 || offset < math.MinInt32) {
						continue
					}
					v[1], _ = Integer(v[1].typ, offset)
					if len(v) > 2 {
						v[2], _ = Integer(v[2].typ, 2)
					}
					_, err := p.Evaluate(context.Background(), v...)
					var state interface{ SQLState() string }
					if !errors.As(err, &state) || state.SQLState() != "2202E" {
						t.Fatal("bounds precede newbit", err)
					}
				}
			}
			if name == "set_bit" {
				v[1], _ = Integer(v[1].typ, 0)
				v[2], _ = Integer(v[2].typ, 2)
				original := v[0]
				_, err := p.Evaluate(context.Background(), v...)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "new bit must be 0 or 1" || v[0] != original {
					t.Fatal("newbit", err)
				}
			}
		})
	}
}

func TestBinaryAccessAllocationsAndCancellation(t *testing.T) {
	for _, name := range []string{"bit_count", "get_bit", "get_byte", "set_bit", "set_byte"} {
		p, v := binaryAccessPlan(t, name)
		v[0], _ = Binary(v[0].typ, strings.Repeat("\x00", 32768))
		original := v[0]
		if got := testing.AllocsPerRun(20, func() {
			if _, err := p.Evaluate(context.Background(), v...); err != nil {
				panic(err)
			}
		}); got != 0 {
			t.Fatal(name, "identity/read allocations", got)
		}
		if len(v) == 3 {
			v[2], _ = Integer(v[2].typ, 1)
			if got := testing.AllocsPerRun(20, func() {
				if _, err := p.Evaluate(context.Background(), v...); err != nil {
					panic(err)
				}
			}); got != 1 {
				t.Fatal(name, "changed allocations", got)
			}
		}
		if name == "bit_count" || len(v) == 3 {
			ctx := &mathCheckpointContext{Context: context.Background(), remaining: 4}
			got, err := p.Evaluate(ctx, v...)
			if !errors.Is(err, context.Canceled) || got != (Value{}) || v[0] != original {
				t.Fatal(name, "bounded cancellation", got, err)
			}
		}
	}
}
