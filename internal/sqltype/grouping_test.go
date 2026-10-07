package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestGroupingConcreteTypes(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id    ID
		a, b  string
		equal bool
	}{
		{Bool, "true", "false", false}, {Int2, "1", "1", true}, {Int4, "1", "2", false}, {Int8, "9223372036854775807", "9223372036854775807", true},
		{Text, "a", "a ", false}, {VarChar, "a", "a ", false}, {BPChar, "a", "a ", true}, {Name, "a", "b", false},
		{Numeric, "1", "1.00", true}, {Numeric, "-0", "0.00", true}, {Numeric, "NaN", "NaN", true}, {Numeric, "Infinity", "-Infinity", false},
		{Float4, "-0", "0", true}, {Float8, "NaN", "NaN", true}, {Float8, "Infinity", "-Infinity", false},
		{Bytea, `\x00ff`, `\x0000`, false}, {Date, "infinity", "infinity", true}, {Date, "-infinity", "infinity", false},
		{Time, "01:00:00", "02:00:00", false}, {Timestamp, "infinity", "infinity", true},
		{UUID, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", false},
		{Regclass, "0", "0", true}, {Regclass, "4294967295", "4294967295", true}, {Regclass, "0", "4294967295", false},
	} {
		t.Run(tc.a+"/"+tc.b+"/"+types[tc.id].Name(), func(t *testing.T) {
			d := types[tc.id]
			input := func(s string) Value {
				if tc.id == Name || tc.id == Regclass {
					return String(d, s)
				}
				v, err := d.Input(ctx, s, -1)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			a, b := input(tc.a), input(tc.b)
			g, err := BindGrouping(d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			equal, err := g.Equal(ctx, a, b)
			if err != nil || equal != tc.equal {
				t.Fatal(equal, err, tc)
			}
			ah, err := g.Hash(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			bh, err := g.Hash(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			if equal && ah != bh {
				t.Fatal("equal key hash mismatch", ah, bh)
			}
			for _, v := range []Value{a, b} {
				same, err := g.Equal(ctx, v, v)
				if !same || err != nil {
					t.Fatal(same, err)
				}
				equal, err = g.Equal(ctx, Null(d), v)
				if equal || err != nil {
					t.Fatal(equal, err)
				}
			}
			equal, err = g.Equal(ctx, Null(d), Null(d))
			if !equal || err != nil {
				t.Fatal(equal, err)
			}
			strict, err := BindComparisonPredicate(CompareEqual, d, d, types[Bool])
			if err != nil {
				t.Fatal(err)
			}
			v, err := strict.Evaluate(ctx, Null(d), Null(d), InputEnvironment{})
			if err != nil || !v.IsNull() {
				t.Fatal(v, err)
			}
		})
	}
	// Different NaN payloads are one SQL key, not Go bit identity.
	g, _ := BindGrouping(types[Float8].Usage())
	a := Value{typ: types[Float8], floating: math.Float64frombits(0x7ff8000000000001)}
	b := Value{typ: types[Float8], floating: math.Float64frombits(0x7ff8000000000002)}
	ah, _ := g.Hash(ctx, a)
	bh, _ := g.Hash(ctx, b)
	same, err := g.Equal(ctx, a, b)
	if !same || err != nil || ah != bh {
		t.Fatal(same, err, ah, bh)
	}
}
func TestGroupingOwnershipAndCancellation(t *testing.T) {
	types := testTypes()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g, _ := BindGrouping(types[Text].Usage())
	a := String(types[Text], "a")
	if _, err := g.Hash(ctx, a); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := g.Equal(ctx, a, a); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := BindGrouping(Usage{}); err == nil {
		t.Fatal("unknown key admitted")
	}
	foreign := testTypes()
	if _, err := g.Hash(context.Background(), String(foreign[Text], "a")); err == nil {
		t.Fatal("foreign descriptor admitted")
	}
	oid, _ := BindGrouping(types[Regclass].Usage())
	for _, s := range []string{"01", "+1", "name", "4294967296", ""} {
		if _, err := oid.Hash(context.Background(), String(types[Regclass], s)); err == nil {
			t.Fatal("noncanonical OID", s)
		}
	}
}

type groupingCancelContext struct {
	context.Context
	calls, at int
}

func (c *groupingCancelContext) Err() error {
	c.calls++
	if c.calls >= c.at {
		return context.Canceled
	}
	return nil
}
func TestGroupingMidHashAndEqualityCancellation(t *testing.T) {
	types := testTypes()
	g, _ := BindGrouping(types[Text].Usage())
	value := String(types[Text], strings.Repeat("x", 2048))
	hashContext := &groupingCancelContext{Context: context.Background(), at: 3}
	if _, err := g.Hash(hashContext, value); !errors.Is(err, context.Canceled) || hashContext.calls != 3 {
		t.Fatal(err, hashContext.calls)
	}
	equalContext := &groupingCancelContext{Context: context.Background(), at: 3}
	if _, err := g.Equal(equalContext, value, value); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
