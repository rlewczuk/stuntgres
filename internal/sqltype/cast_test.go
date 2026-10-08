package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestExplicitCoercionMatrix(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, source := range []ID{Int4, Int8, Text, Bool} {
		for _, target := range []ID{Int4, Int8, Text, Bool} {
			plan, err := PlanCoercion(types[source].Usage(), types[target].Usage(), Explicit)
			unsupported := source == Int8 && target == Bool || source == Bool && target == Int8
			if unsupported {
				if err == nil {
					t.Fatalf("admitted %v -> %v", source, target)
				}
				continue
			}
			if err != nil {
				t.Fatal(source, target, err)
			}
			null, err := plan.Apply(ctx, Null(types[source]), InputEnvironment{}, OutputEnvironment{})
			if err != nil || null.Type() != types[target] || !null.IsNull() {
				t.Fatal(source, target, null, err)
			}
			input := "2"
			if source == Bool || source == Text && target == Bool {
				input = "yes"
			}
			value, err := types[source].Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil || got.Type() != types[target] {
				t.Fatal(source, target, got, err)
			}
			output, err := types[target].Output(ctx, got)
			want := "2"
			if target == Bool {
				want = "t"
			}
			if source == Bool && target == Int4 {
				want = "1"
			}
			if source == Bool && target == Text {
				want = "true"
			}
			if err != nil || output != want {
				t.Fatal(source, target, output, want, err)
			}
		}
	}
	for _, id := range []ID{Int2, Float4, Float8, Numeric, UUID} {
		for _, pair := range [][2]ID{{id, Text}, {Text, id}} {
			_, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "0A000" {
				t.Fatal(pair, err)
			}
		}
	}
	for _, id := range []ID{Date, Time, Timestamp, Timestamptz, Timetz} {
		for _, pair := range [][2]ID{{id, id}, {id, Text}, {Text, id}} {
			if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err != nil {
				t.Fatal(pair, err)
			}
		}
	}
	for _, pair := range [][2]ID{{Bool, Int4}, {Int4, Bool}, {Text, Int4}, {Text, Int8}, {Text, Bool}} {
		for _, mode := range []CoercionContext{Implicit, Assignment} {
			if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), mode); err == nil {
				t.Fatal(pair, mode)
			}
		}
	}
}

func TestExplicitCoercionValueBoundaries(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	plan, _ := PlanCoercion(types[Int4].Usage(), types[Bool].Usage(), Explicit)
	for _, n := range []int64{-2147483648, -2, 0, 2, 2147483647} {
		value, _ := Integer(types[Int4], n)
		got, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		b, accessErr := got.Boolean()
		if err != nil || accessErr != nil || b != (n != 0) {
			t.Fatal(n, got, err, accessErr)
		}
	}
	narrow, _ := PlanCoercion(types[Int8].Usage(), types[Int4].Usage(), Explicit)
	for _, n := range []int64{-2147483649, 2147483648} {
		value, _ := Integer(types[Int8], n)
		_, err := narrow.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Position != "" {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := plan.Apply(canceled, Null(types[Int4]), InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	other := testTypes()
	if _, err := plan.Apply(ctx, Null(other[Int4]), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("foreign descriptor admitted")
	}
}
