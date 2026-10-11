package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestConditionalEqualitySignatures(t *testing.T) {
	types := testTypes()
	for _, id := range []ID{Float4, Float8, Numeric, Bool, Text, BPChar, Name, Date, Time, Timestamp, UUID, Bytea} {
		s, err := ResolveEquality(types[id], types[id])
		if err != nil || s != (EqualitySignature{id, id}) {
			t.Fatal(id, s, err)
		}
	}
	for _, left := range []ID{Int2, Int4, Int8} {
		for _, right := range []ID{Int2, Int4, Int8} {
			s, err := ResolveEquality(types[left], types[right])
			if err != nil || s != (EqualitySignature{left, right}) {
				t.Fatal(left, right, s, err)
			}
		}
	}
	for _, tc := range []struct{ left, right, wantLeft, wantRight ID }{
		{Float4, Float8, Float4, Float8}, {Float8, Float4, Float8, Float4},
		{Int4, Numeric, Numeric, Numeric}, {Numeric, Int8, Numeric, Numeric},
		{Int4, Float4, Float8, Float4}, {Float4, Int4, Float4, Float8},
		{Int2, Float4, Float8, Float4}, {Float4, Int2, Float4, Float8},
		{Int8, Float4, Float8, Float4}, {Float4, Int8, Float4, Float8},
		{Int4, Float8, Float8, Float8}, {Float8, Int4, Float8, Float8},
		{VarChar, VarChar, Text, Text}, {VarChar, Text, Text, Text}, {BPChar, BPChar, BPChar, BPChar},
		{Name, Text, Name, Text}, {Text, Name, Text, Name}, {Date, Timestamp, Date, Timestamp}, {Timestamp, Date, Timestamp, Date},
		{Bool, Bool, Bool, Bool}, {UUID, UUID, UUID, UUID}, {Bytea, Bytea, Bytea, Bytea}, {Time, Time, Time, Time},
	} {
		s, err := ResolveEquality(types[tc.left], types[tc.right])
		if err != nil || s != (EqualitySignature{tc.wantLeft, tc.wantRight}) {
			t.Fatal(tc, s, err)
		}
	}
	for _, tc := range []struct {
		left, right *Descriptor
		want        EqualitySignature
	}{
		{nil, nil, EqualitySignature{Text, Text}}, {nil, types[Int4], EqualitySignature{Int4, Int4}}, {types[Numeric], nil, EqualitySignature{Numeric, Numeric}}, {nil, types[VarChar], EqualitySignature{Text, Text}},
	} {
		s, err := ResolveEquality(tc.left, tc.right)
		if err != nil || s != tc.want {
			t.Fatal(tc, s, err)
		}
	}
	for _, pair := range [][2]ID{{Int4, Bool}, {Date, Time}, {UUID, Bytea}} {
		_, err := ResolveEquality(types[pair[0]], types[pair[1]])
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42883" {
			t.Fatal(pair, err)
		}
	}
}
func TestConditionalTemporalAndNameCoercion(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	plan, err := PlanCoercion(types[Date].Usage(), types[Timestamp].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ day, want int64 }{{0, 0}, {1, microsPerDay}, {-1, -microsPerDay}, {DateNegativeInfinity, TimestampNegativeInfinity}, {DatePositiveInfinity, TimestampPositiveInfinity}} {
		v, err := DateValue(types[Date], tc.day)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		n, err := got.Timestamp()
		if err != nil || n != tc.want {
			t.Fatal(n, tc, err)
		}
	}
	v, _ := DateValue(types[Date], dateMaximum)
	_, err = plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22008" || d.Message != "date out of range for timestamp" {
		t.Fatal(err)
	}
	got, err := plan.Apply(ctx, Null(types[Date]), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !got.IsNull() || got.Type() != types[Timestamp] {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		day, stamp int64
		sign       int
	}{{dateMaximum, 0, 1}, {0, 1, -1}, {-1, -1, -1}, {0, 0, 0}, {DatePositiveInfinity, TimestampPositiveInfinity, 0}, {DateNegativeInfinity, TimestampNegativeInfinity, 0}, {DateNegativeInfinity, 0, -1}, {0, TimestampPositiveInfinity, -1}} {
		a, _ := DateValue(types[Date], tc.day)
		b, _ := TimestampValue(types[Timestamp], tc.stamp)
		p, err := BindComparison(types[Date], types[Timestamp], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		sign, err := p.Compare(ctx, a, b, InputEnvironment{})
		if err != nil || sign != tc.sign {
			t.Fatal(tc, sign, err)
		}
		p, err = BindComparison(types[Timestamp], types[Date], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		sign, err = p.Compare(ctx, b, a, InputEnvironment{})
		if err != nil || sign != -tc.sign {
			t.Fatal(tc, sign, err)
		}
	}
	for _, pair := range [][2]ID{{Name, VarChar}, {Name, BPChar}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Implicit); err == nil {
			t.Fatal("invalid implicit Name edge", pair)
		}
	}
	if _, err := PlanCoercion(types[Name].Usage(), types[Text].Usage(), Implicit); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{Text, VarChar, BPChar} {
		p, err := PlanCoercion(types[id].Usage(), types[Name].Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		v := String(types[id], "x")
		got, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != types[Name] {
			t.Fatal(got, err)
		}
	}
	p, err := PlanCoercion(types[Text].Usage(), types[Name].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	wide := ""
	for range 32 {
		wide += "é"
	}
	got, err = p.Apply(ctx, String(types[Text], wide), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	name, err := got.Text()
	if err != nil || len(name) != 62 {
		t.Fatal(name, err)
	}
}

func TestConditionalNameEqualityProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, pair := range [][2]ID{{Name, Text}, {Text, Name}, {Name, Name}} {
		p, err := BindComparison(types[pair[0]], types[pair[1]], types[pair[0]].Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		a, b := String(types[pair[0]], "same"), String(types[pair[1]], "same")
		sign, err := p.Compare(ctx, a, b, InputEnvironment{})
		if err != nil || sign != 0 {
			t.Fatal(sign, err)
		}
	}
}
