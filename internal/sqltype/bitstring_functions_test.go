package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
	"testing"
)

func bitFunctionPlan(t *testing.T, name string, payloads []string, indexes ...int64) (BinaryPlan, []Value) {
	t.Helper()
	types := map[ID]*Descriptor{}
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	result := Int4
	if name == "bit_count" {
		result = Int8
	}
	if name == "substring" || name == "overlay" || name == "set_bit" {
		result = Bit
	}
	values := make([]Value, 0, len(payloads)+len(indexes))
	usages := make([]Usage, 0, cap(values))
	for _, payload := range payloads {
		value, err := BitString(types[Bit], payload)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
		usages = append(usages, types[Bit].Usage())
	}
	for _, index := range indexes {
		value, err := Integer(types[Int4], index)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
		usages = append(usages, types[Int4].Usage())
	}
	plan, err := BindBinary(name, types[result].Usage(), usages...)
	if err != nil {
		t.Fatal(err)
	}
	return plan, values
}

func TestBitStringFunctionsValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bits    []string
		indexes []int64
		text    string
		number  int64
		code    string
	}{
		{"bit_count", []string{"10111"}, nil, "", 4, ""},
		{"bit_count", []string{"00001111"}, nil, "", 4, ""},
		{"bit_count", []string{"100000001"}, nil, "", 2, ""},
		{"substring", []string{"10110"}, []int64{2}, "0110", 0, ""},
		{"substring", []string{"10110"}, []int64{0, 3}, "10", 0, ""},
		{"substring", []string{"10110"}, []int64{-1, 3}, "1", 0, ""},
		{"substring", []string{"10110"}, []int64{-2, 2}, "", 0, ""},
		{"substring", []string{"10110"}, []int64{3, 0}, "", 0, ""},
		{"substring", []string{"10110"}, []int64{6}, "", 0, ""},
		{"substring", []string{"10110"}, []int64{2, math.MaxInt32}, "0110", 0, ""},
		{"substring", []string{"10110"}, []int64{math.MaxInt32, 1}, "", 0, ""},
		{"substring", []string{"10110"}, []int64{math.MinInt32, math.MaxInt32}, "", 0, ""},
		{"substring", []string{"10110"}, []int64{6, -1}, "", 0, "22011"},
		{"overlay", []string{"10110", "00"}, []int64{3}, "10000", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{3, 0}, "1000110", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{3, -1}, "10000110", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{1, -1}, "0010110", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{9, 1}, "1011000", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{math.MaxInt32, 0}, "1011000", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{math.MaxInt32, 1}, "", 0, "22003"},
		{"overlay", []string{"10110", "00"}, []int64{1, math.MinInt32}, "0010110", 0, ""},
		{"overlay", []string{"10110", "00"}, []int64{0, math.MinInt32}, "", 0, "22011"},
		{"overlay", []string{"10110", ""}, []int64{3}, "10110", 0, ""},
		{"overlay", []string{"10110", ""}, []int64{3, 2}, "100", 0, ""},
		{"position", []string{"", ""}, nil, "", 0, ""},
		{"position", []string{"1", ""}, nil, "", 1, ""},
		{"position", []string{"1", "11"}, nil, "", 0, ""},
		{"position", []string{"111", "11"}, nil, "", 1, ""},
		{"position", []string{"0001", "1"}, nil, "", 4, ""},
		{"position", []string{"111111101", "01"}, nil, "", 8, ""},
		{"position", []string{"000001101011", "010"}, nil, "", 8, ""},
	} {
		p, values := bitFunctionPlan(t, tc.name, tc.bits, tc.indexes...)
		before := append([]Value(nil), values...)
		got, err := p.Evaluate(context.Background(), values...)
		if tc.code != "" {
			var diagnostic *sqlstate.Error
			message := "negative substring length not allowed"
			if tc.code == "22003" {
				message = "integer out of range"
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != message || diagnostic.Position != "" {
				t.Fatal(tc, err)
			}
		} else if err != nil || got.null || got.typ != p.result.Type || got.text != tc.text || got.integer != tc.number {
			t.Fatal(tc, got, err)
		}
		for i := range values {
			if values[i] != before[i] {
				t.Fatal("source mutated")
			}
		}
		// Strict NULL precedes body errors for every argument occurrence.
		for i := range values {
			frame := append([]Value(nil), values...)
			frame[i] = Null(frame[i].typ)
			got, err := p.Evaluate(context.Background(), frame...)
			if err != nil || !got.null || got.typ != p.result.Type {
				t.Fatal(tc, i, got, err)
			}
		}
	}
	for _, width := range []int{0, 1, 7, 8, 9, 16, 17} {
		for _, name := range []string{"length", "bit_length", "octet_length", "bit_count"} {
			p, values := bitFunctionPlan(t, name, []string{strings.Repeat("1", width)})
			got, err := p.Evaluate(context.Background(), values...)
			want := int64(width)
			if name == "octet_length" {
				want = int64((width + 7) / 8)
			}
			if err != nil || got.integer != want {
				t.Fatal(name, width, got, err)
			}
		}
	}
	// The shared matcher still gives BYTEA's empty-empty result one.
	p, values := bitFunctionPlan(t, "position", []string{"", ""})
	var bytea *Descriptor
	for _, d := range Builtins() {
		if d.ID() == Bytea {
			bytea = d
		}
	}
	p, err := BindBinary("position", p.result, bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for i := range values {
		values[i], _ = Binary(bytea, "")
	}
	got, err := p.Evaluate(context.Background(), values...)
	if err != nil || got.integer != 1 {
		t.Fatal(got, err)
	}
}

func TestBitStringFunctionsValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bits    []string
		indexes []int64
	}{
		{"bit_count", []string{"1"}, nil}, {"length", []string{"1"}, nil},
		{"bit_length", []string{"1"}, nil}, {"octet_length", []string{"1"}, nil},
		{"position", []string{"1", "1"}, nil}, {"substring", []string{"1"}, []int64{1}},
		{"substring", []string{"1"}, []int64{1, 1}}, {"overlay", []string{"1", "0"}, []int64{1}},
		{"overlay", []string{"1", "0"}, []int64{1, 1}},
		{"get_bit", []string{"1"}, []int64{0}}, {"set_bit", []string{"1"}, []int64{0, 1}},
	} {
		p, values := bitFunctionPlan(t, tc.name, tc.bits, tc.indexes...)
		args := append([]Usage(nil), p.args[:p.arity]...)
		if !p.Immutable() || p.result.Typmod != -1 {
			t.Fatal(p)
		}
		if _, err := BindBinary(tc.name, p.result); err == nil {
			t.Fatal("zero arity", tc.name)
		}
		tooMany := append(append([]Usage(nil), args...), args[0], args[0], args[0], args[0])
		if _, err := BindBinary(tc.name, p.result, tooMany...); err == nil {
			t.Fatal("excess arity", tc.name)
		}
		for i := range args {
			invalid := append([]Usage(nil), args...)
			invalid[i].Typmod = 3
			if _, err := BindBinary(tc.name, p.result, invalid...); err == nil {
				t.Fatal("typmod", tc.name, i)
			}
			foreign := map[ID]*Descriptor{}
			for _, d := range Builtins() {
				foreign[d.ID()] = d
			}
			frame := append([]Value(nil), values...)
			frame[i].typ = foreign[args[i].Type.ID()]
			if _, err := p.Evaluate(context.Background(), frame...); err == nil {
				t.Fatal("foreign evaluation owner")
			}
			invalid[i] = foreign[args[i].Type.ID()].Usage()
			sameOwnerRequired := p.result.Type.ID() == args[i].Type.ID()
			for j := range args {
				sameOwnerRequired = sameOwnerRequired || i != j && args[i].Type.ID() == args[j].Type.ID()
			}
			if sameOwnerRequired {
				if _, err := BindBinary(tc.name, p.result, invalid...); err == nil {
					t.Fatal("foreign signature owner")
				}
			}
		}
		invalid := p.result
		invalid.Typmod = 2
		if _, err := BindBinary(tc.name, invalid, args...); err == nil {
			t.Fatal("result typmod")
		}
		invalid.Type = args[0].Type
		if p.result.Type.ID() != Bit {
			if _, err := BindBinary(tc.name, invalid, args...); err == nil {
				t.Fatal("result type")
			}
		}
	}
	p, _ := bitFunctionPlan(t, "substring", []string{"1"}, 1)
	for _, name := range []string{"substr", "reverse", "||", "md5"} {
		if _, err := BindBinary(name, p.result, p.args[:p.arity]...); err == nil {
			t.Fatal("adjacent bit admission", name)
		}
	}
}

func TestBitStringFunctionsCancellation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bits        []string
		indexes     []int64
		checkpoints int
	}{
		{"bit_count", []string{strings.Repeat("1", 65536)}, nil, 4},
		// A short absent needle completes factorization before cancellation; the
		// seventh context checkpoint falls inside actual haystack comparisons.
		{"position", []string{strings.Repeat("01", 32768), "01010101011"}, nil, 7},
		{"overlay", []string{strings.Repeat("1", 65536), strings.Repeat("0", 65536)}, []int64{32769, 0}, 4},
	} {
		p, values := bitFunctionPlan(t, tc.name, tc.bits, tc.indexes...)
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: tc.checkpoints}
		got, err := p.Evaluate(ctx, values...)
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal(tc.name, got, err)
		}
		got, err = p.Evaluate(context.Background(), values...)
		if err != nil || got.typ != p.result.Type {
			t.Fatal("recovery", tc.name, got, err)
		}
	}
}

func TestBitStringFunctionsPayloadAllocations(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bits        []string
		indexes     []int64
		allocations int64
	}{
		{"length", []string{strings.Repeat("1", 32768)}, nil, 0},
		{"octet_length", []string{strings.Repeat("1", 32769)}, nil, 0},
		{"bit_count", []string{strings.Repeat("1", 32768)}, nil, 0},
		{"substring", []string{strings.Repeat("1", 32768)}, []int64{2, 100}, 0},
		{"position", []string{strings.Repeat("01", 16384), "10"}, nil, 0},
		{"overlay", []string{strings.Repeat("1", 32768), ""}, []int64{2}, 0},
		{"overlay", []string{strings.Repeat("1", 32768), "0"}, []int64{2}, 1},
	} {
		p, values := bitFunctionPlan(t, tc.name, tc.bits, tc.indexes...)
		allocations, _ := util.AllocationsPerCall(64, func() {
			if _, err := p.Evaluate(context.Background(), values...); err != nil {
				t.Fatal(err)
			}
		})
		if allocations != tc.allocations {
			t.Fatal(tc.name, "payload allocations", allocations, tc.allocations)
		}
	}
}
