package sqltype

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/sqlstate"
)

func castUnitError(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message {
		t.Fatalf("diagnostic=%v want %s %s", err, code, message)
	}
}
func castUnitValue(t *testing.T, d *Descriptor, input string, env InputEnvironment) Value {
	t.Helper()
	v, e := d.InputWithEnvironment(context.Background(), input, -1, env)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func castUnitOutput(t *testing.T, v Value, env OutputEnvironment, want string) {
	t.Helper()
	got, e := v.Type().OutputWithEnvironment(context.Background(), v, env)
	if e != nil || got != want {
		t.Fatalf("output=%q err=%v want %q", got, e, want)
	}
}

type castUnitDomainRule struct{ negative bool }

func (r castUnitDomainRule) CheckDomain(ctx context.Context, d *Descriptor, v Value) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if v.IsNull() {
		if d.DomainNotNull() {
			return failure("23502", "domain "+d.Name()+" does not allow null values")
		}
		return nil
	}
	if d.HasDomainChecks() {
		n, e := v.Integer()
		if e != nil {
			return e
		}
		if !r.negative && n <= 0 || r.negative && n >= 0 {
			return failure("23514", "positive integer constraint")
		}
	}
	return nil
}
func TestCastImplementedConversions(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	zone, e := ResolveZone("UTC")
	if e != nil {
		t.Fatal(e)
	}
	input := InputEnvironment{Zone: zone}
	output := OutputEnvironment{Zone: zone}
	for _, tc := range []struct {
		s, t                         ID
		input, output, code, message string
		mod                          int32
	}{
		{Int4, Bytea, "42", `\x0000002a`, "", "", -1}, {Int2, Bytea, "-1", `\xffff`, "", "", -1}, {Int8, Bytea, "-1", `\xffffffffffffffff`, "", "", -1},
		{Bytea, Int2, `\xff`, "255", "", "", -1}, {Bytea, Int2, `\xffff`, "-1", "", "", -1}, {Bytea, Int4, `\x`, "0", "", "", -1}, {Bytea, Int2, `\x000001`, "", "22003", "smallint out of range", -1},
		{Int4, Bool, "-2", "t", "", "", -1}, {Bool, Int4, "true", "1", "", "", -1},
		{JSONB, Int4, "2.5", "3", "", "", -1}, {JSONB, Int4, "-2.5", "-3", "", "", -1}, {JSONB, Bool, "true", "t", "", "", -1},
		{JSONB, Int4, `"1"`, "", "22023", "cannot cast jsonb string to type integer", -1}, {JSONB, Int2, "32768", "", "22003", "smallint out of range", -1},
		{Macaddr, Macaddr8, "00:34:56:ab:cd:ef", "00:34:56:ff:fe:ab:cd:ef", "", "", -1},
		{Macaddr8, Macaddr, "00:34:56:ff:fe:ab:cd:ef", "00:34:56:ab:cd:ef", "", "", -1},
		{Interval, Time, "-2 hours", "22:00:00", "", "", -1}, {Interval, Time, "infinity", "", "22008", "cannot convert infinite interval to time", -1},
		{Int4, Bit, "5", "0101", "", "", 4}, {Bit, Int4, "11111111111111111111111111111111", "-1", "", "", -1},
		{Lseg, Point, "[(2,4),(8,10)]", "(5,7)", "", "", -1}, {Point, Box, "(2,3)", "(2,3),(2,3)", "", "", -1},
		{Int4Range, Int4Multirange, "[1,3)", "{[1,3)}", "", "", -1}, {Int4Range, Int4Multirange, "empty", "{}", "", "", -1},
		{Char, Int4, `\377`, "-1", "", "", -1}, {Int4, Char, "-1", `\377`, "", "", -1},
		{Text, Char, `\377`, `\377`, "", "", -1}, {Text, Char, "", "", "", "", -1},
		{Char, VarChar, `\377`, `\377`, "", "", -1}, {Char, BPChar, "a", "a  ", "", "", 7},
		{Int4, Char, "128", "", "22003", `"char" out of range`, -1},
		{Int4, Char, "-128", `\200`, "", "", -1}, {Int4, Char, "-129", "", "22003", `"char" out of range`, -1},
		{Int4, Char, "0", "", "", "", -1}, {Int4, Char, "65", "A", "", "", -1}, {Int4, Char, "127", "\x7f", "", "", -1},
		{Char, Int4, `\200`, "-128", "", "", -1}, {Char, Int4, "", "0", "", "", -1},
		{Char, BPChar, "", "\x00", "", "", -1}, {Char, BPChar, `\377`, "\xff", "", "", -1},
		{Bit, Int4, "100000000000000000000000000000000", "", "22003", "integer out of range", -1},
		{Text, Bit, "1011", "10", "", "", 2}, {Text, VarBit, "1011", "10", "", "", 2},
		{Interval, Time, "2 mons 3 days -2 hours", "22:00:00", "", "", -1},
		{JSONB, Bool, "2", "", "22023", "cannot cast jsonb numeric to type boolean", -1},
		{JSONB, Int4, "[]", "", "22023", "cannot cast jsonb array to type integer", -1},
	} {
		t.Run(fmt.Sprintf("%d_%d_%s", tc.s, tc.t, tc.input), func(t *testing.T) {
			target := types[tc.t].Usage()
			target.Typmod = tc.mod
			p, err := PlanCoercion(types[tc.s].Usage(), target, Explicit)
			if err != nil {
				t.Fatal(err)
			}
			source := castUnitValue(t, types[tc.s], tc.input, input)
			before, err := source.Type().OutputWithEnvironment(ctx, source, output)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Apply(ctx, source, input, output)
			if tc.code != "" {
				castUnitError(t, err, tc.code, tc.message)
				if got.Type() != nil {
					t.Fatal("partial value escaped failed conversion")
				}
			} else {
				if err != nil || got.Type() != target.Type || got.IsNull() {
					t.Fatal(got, err)
				}
				castUnitOutput(t, got, output, tc.output)
			}
			castUnitOutput(t, source, output, before)
			null, err := p.Apply(ctx, Null(types[tc.s]), input, output)
			if err != nil || !null.IsNull() || null.Type() != target.Type {
				t.Fatal("typed NULL", null, err)
			}
		})
	}
	p, err := PlanCoercion(types[JSONB].Usage(), types[Int4].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Apply(ctx, castUnitValue(t, types[JSONB], "null", input), input, output)
	if err != nil || !got.IsNull() || got.Type() != types[Int4] {
		t.Fatal("JSON null did not become typed SQL NULL", got, err)
	}
	positive, err := Domain(49000, "cast_unit_positive", "public.cast_unit_positive", types[Int4].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	required, err := Domain(49001, "cast_unit_required", "public.cast_unit_required", types[Int4].Usage(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	input.DomainChecks = castUnitDomainRule{}
	entry, err := PlanCoercion(types[Int4].Usage(), positive.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	source := castUnitValue(t, types[Int4], "1", input)
	value, err := entry.Apply(ctx, source, input, output)
	if err != nil || value.Type() != positive {
		t.Fatal(value, err)
	}
	bad := castUnitValue(t, types[Int4], "0", input)
	_, err = entry.Apply(ctx, bad, input, output)
	castUnitError(t, err, "23514", "positive integer constraint")
	if !entry.ChecksDomain() {
		t.Fatal("new domain entry omitted its required check property")
	}
	identity, err := PlanCoercion(positive.Usage(), positive.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	peel, err := PlanCoercion(positive.Usage(), types[Int4].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	input.DomainChecks = castUnitDomainRule{negative: true}
	for _, plan := range []CoercionPlan{identity, peel} {
		v, e := plan.Apply(ctx, value, input, output)
		if e != nil {
			t.Fatal("existing identity/downcast rechecked", e)
		}
		base := v
		if v.Type().IsDomain() {
			base, e = v.BaseValue()
			if e != nil {
				t.Fatal(e)
			}
		}
		n, e := base.Integer()
		if e != nil || n != 1 || plan.ChecksDomain() {
			t.Fatal(v, e)
		}
	}
	notnull, err := PlanCoercion(types[Int4].Usage(), required.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	_, err = notnull.Apply(ctx, Null(types[Int4]), input, output)
	castUnitError(t, err, "23502", "domain cast_unit_required does not allow null values")
}
