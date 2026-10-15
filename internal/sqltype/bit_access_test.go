package sqltype

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestBitAccessValues(t *testing.T) {
	for _, tc := range []struct {
		source             string
		index, bit, number int64
		changed            string
	}{
		{"1", 0, 0, 1, "0"},
		{"1001010", 6, 1, 0, "1001011"},
		{"10010100", 7, 1, 0, "10010101"},
		{"100000001", 0, 0, 1, "000000001"},
		{"100000001", 1, 1, 0, "110000001"},
		{"100000001", 8, 0, 1, "100000000"},
		{"000000000", 8, 1, 0, "000000001"},
		{"1001010000010100", 15, 1, 0, "1001010000010101"},
		{"10010100000101001", 16, 0, 1, "10010100000101000"},
		{"101010101010101010", 6, 0, 1, "101010001010101010"},
		{"001", 1, 0, 0, "001"},
	} {
		for _, name := range []string{"get_bit", "set_bit"} {
			indexes := []int64{tc.index}
			if name == "set_bit" {
				indexes = append(indexes, tc.bit)
			}
			p, values := bitFunctionPlan(t, name, []string{tc.source}, indexes...)
			original := values[0]
			got, err := p.Evaluate(context.Background(), values...)
			if err != nil || got.typ != p.result.Type || got.null {
				t.Fatal(tc, name, got, err)
			}
			if name == "get_bit" && got.integer != tc.number || name == "set_bit" && got.text != tc.changed {
				t.Fatal(tc, name, got)
			}
			if values[0] != original {
				t.Fatal("mutated source", tc, name)
			}
		}
	}
}

func TestBitAccessValidation(t *testing.T) {
	for _, name := range []string{"get_bit", "set_bit"} {
		for _, source := range []string{"", "101"} {
			for _, index := range []int64{-1, int64(len(source)), math.MinInt32, math.MaxInt32} {
				indexes := []int64{index}
				if name == "set_bit" {
					indexes = append(indexes, 2)
				}
				p, values := bitFunctionPlan(t, name, []string{source}, indexes...)
				got, err := p.Evaluate(context.Background(), values...)
				var d *sqlstate.Error
				want := fmt.Sprintf("bit index %d out of valid range (0..%d)", index, len(source)-1)
				if got != (Value{}) || !errors.As(err, &d) || d.Code != "2202E" || d.Message != want || d.Position != "" || d.Context != "" {
					t.Fatal(name, got, err)
				}
				for i := range values {
					frame := append([]Value(nil), values...)
					frame[i] = Null(frame[i].typ)
					got, err := p.Evaluate(context.Background(), frame...)
					if err != nil || !got.null || got.typ != p.result.Type {
						t.Fatal("NULL before bounds/value", name, i, got, err)
					}
				}
			}
		}
		indexes := []int64{0}
		if name == "set_bit" {
			indexes = append(indexes, 1)
		}
		p, values := bitFunctionPlan(t, name, []string{"1"}, indexes...)
		args := append([]Usage(nil), p.args[:p.arity]...)
		if got, err := (BinaryPlan{}).Evaluate(context.Background(), values...); err == nil || got != (Value{}) {
			t.Fatal("zero plan", got, err)
		}
		if got, err := p.Evaluate(context.Background()); err == nil || got != (Value{}) {
			t.Fatal("arity", got, err)
		}
		for i := range values {
			frame := append([]Value(nil), values...)
			frame[i] = Value{}
			if got, err := p.Evaluate(context.Background(), frame...); err == nil || got != (Value{}) {
				t.Fatal("zero value", i, got, err)
			}
			invalid := append([]Usage(nil), args...)
			invalid[i] = Usage{}
			if _, err := BindBinary(name, p.result, invalid...); err == nil {
				t.Fatal("zero argument usage", i)
			}
		}
		foreign := map[ID]*Descriptor{}
		for _, d := range Builtins() {
			foreign[d.ID()] = d
		}
		invalid := append([]Usage(nil), args...)
		invalid[1] = foreign[Int8].Usage()
		if _, err := BindBinary(name, p.result, invalid...); err == nil {
			t.Fatal("INT8 BIT index")
		}
		if _, err := BindBinary(name, Usage{}, args...); err == nil {
			t.Fatal("zero result usage")
		}
		wrong := args[0]
		if name == "set_bit" {
			wrong = args[1]
		}
		if _, err := BindBinary(name, wrong, args...); err == nil {
			t.Fatal("wrong result ID")
		}
		if name == "get_bit" {
			// A single INT4 result and index must share descriptor ownership.
			if _, err := BindBinary(name, foreign[Int4].Usage(), args...); err == nil {
				t.Fatal("foreign getter result owner")
			}
		} else {
			invalid = append([]Usage(nil), args...)
			invalid[2] = foreign[Int4].Usage()
			if _, err := BindBinary(name, p.result, invalid...); err == nil {
				t.Fatal("split INT4 owners")
			}
			for _, bit := range []int64{-1, 2, math.MinInt32, math.MaxInt32} {
				values[2], _ = Integer(values[2].typ, bit)
				before := values[0]
				got, err := p.Evaluate(context.Background(), values...)
				var d *sqlstate.Error
				if got != (Value{}) || !errors.As(err, &d) || d.Code != "22023" || d.Message != "new bit must be 0 or 1" || values[0] != before {
					t.Fatal("newbit", bit, got, err)
				}
			}
		}
	}
}

func TestBitAccessAllocationsAndCancellation(t *testing.T) {
	for _, name := range []string{"get_bit", "set_bit"} {
		indexes := []int64{32768}
		if name == "set_bit" {
			indexes = append(indexes, 0)
		}
		p, values := bitFunctionPlan(t, name, []string{strings.Repeat("0", 65536)}, indexes...)
		original := values[0]
		if allocations := testing.AllocsPerRun(20, func() {
			if _, err := p.Evaluate(context.Background(), values...); err != nil {
				panic(err)
			}
		}); allocations != 0 {
			t.Fatal(name, "read/no-op allocations", allocations)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := p.Evaluate(ctx, values...)
		if !errors.Is(err, context.Canceled) || got != (Value{}) || values[0] != original {
			t.Fatal("pre-cancel", name, got, err)
		}
		frame := append([]Value(nil), values...)
		frame[0] = Null(frame[0].typ)
		if got, err := p.Evaluate(ctx, frame...); !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancel before NULL", got, err)
		}
		if name == "set_bit" {
			values[2], _ = Integer(values[2].typ, 1)
			if allocations := testing.AllocsPerRun(20, func() {
				if _, err := p.Evaluate(context.Background(), values...); err != nil {
					panic(err)
				}
			}); allocations != 1 {
				t.Fatal("changed payload allocations", allocations)
			}
			// Checkpoint 4 interrupts prefix copying after two bounded chunks;
			// checkpoint 12 interrupts suffix copying, never publishing a prefix.
			for _, checkpoint := range []int{4, 12} {
				ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
				got, err := p.Evaluate(ctx, values...)
				if !errors.Is(err, context.Canceled) || got != (Value{}) || values[0] != original {
					t.Fatal("copy cancellation", checkpoint, got, err)
				}
			}
		}
		if got, err := p.Evaluate(context.Background(), values...); err != nil || got.typ != p.result.Type || values[0] != original {
			t.Fatal("recovery", name, got, err)
		}
	}
}
