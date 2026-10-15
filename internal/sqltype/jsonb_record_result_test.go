package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONBRecordResultPlan(t *testing.T) {
	types := testTypes()
	source, err := Composite(62000, "actual_record", "public.actual_record", 62001, []CompositeMember{{Attribute: 2, Name: "actual_a", Usage: types[Int4].Usage()}, {Attribute: 9, Name: "actual_b", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "b", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	integer, err := Integer(types[Int4], 7)
	if err != nil {
		t.Fatal(err)
	}
	value, err := OwnedCompositeValue(source, []Value{integer, String(types[Text], "keep")})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanJSONBRecordResult(source, target, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply(context.Background(), value, InputEnvironment{})
	if err != nil || result.typ != target || result.composite != value.composite || value.typ != source || result.CompositeField(0) != integer || result.CompositeField(1) != value.CompositeField(1) {
		t.Fatal("ordinal relabel copied/reordered/modified immutable payload", result, err)
	}
	if null, err := plan.Apply(context.Background(), Null(source), InputEnvironment{}); err != nil || !null.IsNull() || null.Type() != target {
		t.Fatal(null, err)
	}
	empty, err := RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanJSONBRecordResult(empty, target, InputEnvironment{})
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "function return row and query-specified return row do not match", Detail: "Returned row contains 0 attributes, but query expects 2."}
	if !errors.As(err, &diagnostic) || *diagnostic != want || JSONBRecordSoftError(err) {
		t.Fatal("caller arity is a hard error", err)
	}
	wide, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: types[Int8].Usage()}, {Attribute: 2, Name: "b", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanJSONBRecordResult(source, wide, InputEnvironment{TypeDiagnosticName: func(ID) string { return "int4" }})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Detail != "Returned type integer at ordinal position 1, but query expects bigint." {
		t.Fatal("caller ABI became a numeric cast", err)
	}
	foreign := testTypes()
	other, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: foreign[Int4].Usage()}, {Attribute: 2, Name: "b", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanJSONBRecordResult(source, other, InputEnvironment{}); err == nil || JSONBRecordSoftError(err) {
		t.Fatal("foreign field generation accepted", err)
	}
	if _, err := plan.Apply(context.Background(), Null(target), InputEnvironment{}); err == nil {
		t.Fatal("foreign tuple generation accepted")
	}
	varchar3, varchar1 := types[VarChar].Usage(), types[VarChar].Usage()
	varchar3.Typmod, varchar1.Typmod = 7, 5
	minusOne, err := Integer(types[Int4], -1)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := Domain(62010, "result_domain", "public.result_domain", types[Int4].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(integer)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		from, to Usage
		value    Value
		want     string
		shared   bool
	}{
		{"text varchar", types[Text].Usage(), varchar1, String(types[Text], "abc"), "abc", false},
		{"varchar source sizing", varchar3, varchar1, String(types[VarChar], "abc"), "abc", true},
		{"varchar text", varchar3, types[Text].Usage(), String(types[VarChar], "abc"), "abc", false},
		{"oid carrier bits", types[Int4].Usage(), types[Oid].Usage(), minusOne, "4294967295", false},
		{"domain base", domain.Usage(), types[Int4].Usage(), wrapped, "7", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "actual", Usage: tc.from}})
			if err != nil {
				t.Fatal(err)
			}
			caller, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: tc.to}})
			if err != nil {
				t.Fatal(err)
			}
			input, err := OwnedCompositeValue(actual, []Value{tc.value})
			if err != nil {
				t.Fatal(err)
			}
			binary, err := PlanJSONBRecordResult(actual, caller, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := binary.Apply(context.Background(), input, InputEnvironment{})
			if err != nil || result.Type() != caller || result.CompositeField(0).Type() != tc.to.Type {
				t.Fatal(result, err)
			}
			text, err := tc.to.Type.Output(context.Background(), result.CompositeField(0))
			if err != nil || text != tc.want {
				t.Fatal("carrier or source typmod changed", text, err)
			}
			if (result.composite == input.composite) != tc.shared || input.CompositeField(0) != tc.value {
				t.Fatal("binary return changed source ownership or copied label-only payload")
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := binary.Apply(cancelled, input, InputEnvironment{}); !errors.Is(err, context.Canceled) {
				t.Fatal("return plan swallowed cancellation", err)
			}
		})
	}
	for _, pair := range [][2]*Descriptor{{types[Int4], types[Int8]}, {types[BPChar], types[Text]}, {types[Int4], domain}, {types[types[Text].AssociatedArray()], types[types[VarChar].AssociatedArray()]}} {
		actual, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "actual", Usage: pair[0].Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		caller, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: pair[1].Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := PlanJSONBRecordResult(actual, caller, InputEnvironment{}); !errors.As(err, &diagnostic) || diagnostic.Code != "42804" {
			t.Fatal("nonbinary field admitted", pair, err)
		}
	}
}
