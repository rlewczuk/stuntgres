package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestMultirangeDescriptors(t *testing.T) {
	types := testTypes()
	for _, family := range []struct {
		companion, rangeID, subtype, array ID
		name                               string
	}{
		{Int4Multirange, Int4Range, Int4, 6150, "INT4MULTIRANGE"},
		{Int8Multirange, Int8Range, Int8, 6157, "INT8MULTIRANGE"},
		{NumMultirange, NumRange, Numeric, 6151, "NUMMULTIRANGE"},
	} {
		d := types[family.companion]
		if !d.Concrete() || d.DatabaseName() != family.name || d.SQLName() != family.name || d.AssociatedArray() != family.array || d.multirangeRange != types[family.rangeID] || d.multirangeRange.rangeSubtype != types[family.subtype] || types[family.array].Concrete() || types[family.array].element != d || StoredScalarCastType(d.id) || d.Usage().Typmod != -1 || d.Usage().Collation != (CollationRef{}) {
			t.Fatal("companion generation", d)
		}
	}
	for _, id := range []ID{4533, 4534, 4535, 6152, 6155, 6153} {
		if types[id].Concrete() {
			t.Fatal("unrelated family admitted", id)
		}
	}
}

func TestMultirangeCanonicalDatum(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		d := testTypes()[id]
		for _, tc := range [][2]string{
			{"{[1,3),[2,4),empty,[8,9)}", "{[1,4),[8,9)}"},
			{"{[8,9),[1,3),[3,8)}", "{[1,9)}"},
			{"{[1,10),[2,3),[1,10)}", "{[1,10)}"},
			{"{[1,2),[3,4)}", "{[1,2),[3,4)}"},
			{"{[1,1),empty}", "{}"}, {"{}", "{}"}, {"{[,],[1,4)}", "{(,)}"},
			{"{(,2),[2,)}", "{(,)}"},
		} {
			v, err := d.Input(ctx, tc[0], -1)
			if err != nil || v.IsNull() || !v.validMultirange() {
				t.Fatal(tc, v, err)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc[1] {
				t.Fatal(tc, out, err)
			}
			canonical, err := d.Input(ctx, out, -1)
			if err != nil || !SameDatum(v, canonical) {
				t.Fatal("canonical representation", tc, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "'"+out+"'" {
				t.Fatal(literal, err)
			}
		}
	}
	d := testTypes()[NumMultirange]
	for _, tc := range [][2]string{
		{"{(4,5)}", "{(4,5)}"},
		{"{[1.0,2.00),[2.000,3.0)}", "{[1.0,3.0)}"},
		{"{[1,2),(2,3]}", "{[1,2),(2,3]}"},
		{"{[1,2],(2,3]}", "{[1,3]}"},
		{"{[1.0,4.00),[1.000,3.0)}", "{[1.0,4.00)}"},
		{"{[1.0,3.00),[2,3.000)}", "{[1.0,3.000)}"},
		{"{[1.0,3.00),[1.000,4.0)}", "{[1.000,4.0)}"},
		{"{[1.000,4.0),[1.0,3.00)}", "{[1.000,4.0)}"},
		{"{[1.0,4.00),[2.000,4.000)}", "{[1.0,4.000)}"},
		{"{[2.000,4.000),[1.0,4.00)}", "{[1.0,4.000)}"},
		{"{[9007199254740992.2,9007199254740992.3),[9007199254740992.1,9007199254740992.2)}", "{[9007199254740992.1,9007199254740992.3)}"},
		{"{[-Infinity,Infinity],[Infinity,NaN]}", "{[-Infinity,NaN]}"},
	} {
		v, err := d.Input(ctx, tc[0], -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc[1] {
			t.Fatal(tc, out, err)
		}
	}
	a, _ := d.Input(ctx, "{[1.0,2.00)}", -1)
	b, _ := d.Input(ctx, "{[1.00,2.0)}", -1)
	if SameDatum(a, b) {
		t.Fatal("display scale erased")
	}
}

func TestMultirangeOwnershipAndValidation(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		d, foreign := testTypes()[id], testTypes()[id]
		v, err := d.Input(ctx, "{[1,3),[8,9)}", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := foreign.Output(ctx, v); err == nil {
			t.Fatal("foreign output admitted")
		}
		if _, err := foreign.SQLLiteral(ctx, v); err == nil {
			t.Fatal("foreign literal admitted")
		}
		reminted, err := v.Retag(foreign)
		if err != nil || reminted.typ != foreign || reminted.multirange != v.multirange {
			t.Fatal("payload-copying retag", err)
		}
		if _, err := v.Retag(testTypes()[Int4]); err == nil {
			t.Fatal("wrong identity retag")
		}
		empty, _ := d.Input(ctx, "{}", -1)
		otherEmpty, _ := d.Input(ctx, "{empty}", -1)
		if empty.multirange == nil || !SameDatum(empty, otherEmpty) || SameDatum(empty, Null(d)) {
			t.Fatal("empty/NULL distinction")
		}
		if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" {
			t.Fatal(literal, err)
		}
		if _, err := d.Output(ctx, Null(d)); err == nil {
			t.Fatal("NULL output admitted")
		}
		first, second := v.multirange.members[0], v.multirange.members[1]
		for _, payload := range []*multirangePayload{
			nil,
			{[]rangeDatum{{flags: rangeEmpty}}},
			{[]rangeDatum{second, first}},
			{[]rangeDatum{first, first}},
			{[]rangeDatum{{lower: rangeBound{magnitude: 5}, upper: rangeBound{magnitude: 1}, flags: rangeLowerInclusive}}},
			{[]rangeDatum{{flags: 128}}},
		} {
			bad := Value{typ: d, multirange: payload}
			if _, err := d.Output(ctx, bad); err == nil || SameDatum(bad, bad) {
				t.Fatal("invalid payload admitted", payload, err)
			}
			if _, err := bad.Retag(foreign); err == nil {
				t.Fatal("invalid retag admitted")
			}
		}
		mergeable, _ := d.multirangeRange.Input(ctx, "[3,8)", -1)
		bad := Value{typ: d, multirange: &multirangePayload{[]rangeDatum{first, mergeable.rangeDatum, second}}}
		if bad.validMultirange() {
			t.Fatal("adjacent members admitted")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		for _, call := range []func() error{
			func() error { _, err := d.Input(canceled, "{}", -1); return err },
			func() error { _, err := d.Output(canceled, v); return err },
			func() error { _, err := d.SQLLiteral(canceled, v); return err },
		} {
			if !errors.Is(call(), context.Canceled) {
				t.Fatal("cancellation lost")
			}
		}
	}
}

func TestMultirangeStreamingDiagnostics(t *testing.T) {
	d := testTypes()[Int4Multirange]
	for _, tc := range []struct{ input, code, message, detail string }{
		{"{empty,}", "22P02", `malformed multirange literal: "{empty,}"`, "Expected range start."},
		{"{[oops,4", "22P02", `malformed multirange literal: "{[oops,4"`, "Unexpected end of input."},
		{"{[oops,4)}junk", "22P02", `invalid input syntax for type integer: "oops"`, ""},
		{"{[4,1),[oops,4)}", "22000", "range lower bound must be less than or equal to range upper bound", ""},
	} {
		_, err := d.Input(context.Background(), tc.input, -1)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%q got=%#v want=%#v err=%v", tc.input, got, want, err)
		}
	}
}

func TestMultirangeDomainGeneration(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		first, second := testTypes()[id], testTypes()[id]
		makeNested := func(base *Descriptor) *Descriptor {
			span, err := Domain(900001, "span", "public.span", base.Usage())
			if err != nil {
				t.Fatal(err)
			}
			chain, err := Domain(900002, "chain", "public.chain", span.Usage())
			if err != nil {
				t.Fatal(err)
			}
			return chain
		}
		d, foreign := makeNested(first), makeNested(second)
		v, err := d.Input(ctx, "{[1,3),[2,4)}", -1)
		if err != nil {
			t.Fatal(err)
		}
		retag, err := v.Retag(foreign)
		if err != nil || retag.multirange != v.multirange || retag.Type() != foreign {
			t.Fatal("nested generation payload remap", err)
		}
		out, err := foreign.Output(ctx, retag)
		if err != nil || out != "{[1,4)}" {
			t.Fatal(out, err)
		}
		invalid := *second
		invalid.multirangeRange = nil
		baseValue, err := first.Input(ctx, "{}", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := baseValue.Retag(&invalid); err == nil {
			t.Fatal("invalid destination linkage admitted")
		}
		if first.multirangeRange.multirangeCompanion != first || second.multirangeRange.multirangeCompanion != second {
			t.Fatal("foreign companion generation")
		}
		if _, err := first.Input(ctx, "{}", 0); err == nil {
			t.Fatal("typmod admitted")
		}
		for _, input := range []string{"{}\x00", "{[\xff,4)}"} {
			if _, err := first.Input(ctx, input, -1); err == nil {
				t.Fatal("invalid character input admitted")
			}
		}
	}
}

func TestMultirangeComparisonAdmission(t *testing.T) {
	types := testTypes()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "incompatible comparison operands"}
	assertRejected := func(err error) {
		t.Helper()
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("comparison admission diagnostic = %#v (%v), want %#v", got, err, want)
		}
	}
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		base := types[id]
		span, err := Domain(900001, "span", "public.span", base.Usage())
		if err != nil {
			t.Fatal(err)
		}
		chain, err := Domain(900002, "chain", "public.chain", span.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, left := range []*Descriptor{base, span, chain} {
			for _, right := range []*Descriptor{base, span, chain, types[Int4], types[Text]} {
				for _, pair := range [][2]*Descriptor{{left, right}, {right, left}} {
					_, err := BindComparison(pair[0], pair[1], pair[0].Usage().Collation, false)
					assertRejected(err)
					for op := CompareEqual; op <= CompareGreaterEqual; op++ {
						_, err := BindComparisonPredicate(op, pair[0], pair[1], types[Bool], pair[0].Usage().Collation, false)
						assertRejected(err)
					}
				}
			}
		}
	}
	for _, pair := range [][2]ID{{Int4, Int8}, {Text, Text}, {Numeric, Numeric}} {
		if _, err := BindComparison(types[pair[0]], types[pair[1]], types[pair[0]].Usage().Collation, false); err != nil {
			t.Fatal("supported comparison changed", pair, err)
		}
		for op := CompareEqual; op <= CompareGreaterEqual; op++ {
			if _, err := BindComparisonPredicate(op, types[pair[0]], types[pair[1]], types[Bool], types[pair[0]].Usage().Collation, false); err != nil {
				t.Fatal("supported predicate changed", pair, op, err)
			}
		}
	}
}
