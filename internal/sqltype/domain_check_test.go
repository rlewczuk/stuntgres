package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestDomainCheckInputVersusIdentity(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	integer := types[Int4]
	checked, err := Domain(49000, "checked", "public.checked", integer.Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := Domain(49001, "inherited", "public.inherited", checked.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if !checked.HasDomainChecks() || !inherited.HasDomainChecks() || !ContainsDomainChecks(inherited) {
		t.Fatal("lost check ancestry")
	}
	input, err := PlanCoercion(integer.Usage(), checked.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.Apply(ctx, Null(integer), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("constrained fresh NULL silently accepted without evaluator")
	}
	value, err := checked.Wrap(Null(integer))
	if err != nil {
		t.Fatal(err)
	}
	same, err := PlanCoercion(checked.Usage(), checked.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	out, err := same.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
	if err != nil || out.Type() != checked || !out.IsNull() {
		t.Fatal("same-domain NULL must bypass conversion", out, err)
	}
	downcast, err := PlanCoercion(checked.Usage(), integer.Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	out, err = downcast.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
	if err != nil || out.Type() != integer || !out.IsNull() {
		t.Fatal("downcast unexpectedly checked", out, err)
	}
	unchecked, err := Domain(49002, "unchecked", "public.unchecked", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := inherited.RebindDomain(unchecked.Usage(), "public.inherited")
	if err != nil || rebound.HasDomainChecks() || !inherited.HasDomainChecks() {
		t.Fatal("immutable inherited marker recomputation", rebound, err)
	}
	if _, err := checked.InputWithEnvironment(ctx, "1", -1, InputEnvironment{}); err == nil {
		t.Fatal("fresh text input lacks live evaluator")
	}
	physical, err := Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := checked.Wrap(physical)
	if err != nil || wrapped.Type() != checked {
		t.Fatal("Wrap must not become global CHECK enforcement", wrapped, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := input.Apply(canceled, Null(integer), InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled fresh conversion", err)
	}
}

func TestDomainCheckNestedInputAndTypmod(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	checked, err := Domain(49100, "member", "public.member", types[Int4].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(49101, "_member", checked)
	if err != nil {
		t.Fatal(err)
	}
	record, err := Composite(49102, "wrapper", "public.wrapper", 49103, []CompositeMember{{Name: "v", Attribute: 1, Usage: checked.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ  *Descriptor
		text string
	}{{array, "{NULL}"}, {record, "()"}} {
		if !ContainsDomainChecks(tc.typ) {
			t.Fatal("nested type lost checked-member authority")
		}
		if _, err := tc.typ.InputWithEnvironment(ctx, tc.text, -1, InputEnvironment{}); err == nil {
			t.Fatal("live member NULL escaped check", tc.text)
		}
	}
	usage, _, err := types[VarChar].ResolveModifiers(4)
	if err != nil {
		t.Fatal(err)
	}
	short, err := Domain(49104, "short", "public.short", usage, true)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCoercion(types[Text].Usage(), short.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Apply(ctx, String(types[Text], "abcde"), InputEnvironment{}, OutputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22001" {
		t.Fatal("typmod must precede checks", err)
	}
}

func TestDomainCheckUnconstrainedAllocationFree(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	plain, err := Domain(49200, "plain", "public.plain", types[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCoercion(types[Int4].Usage(), plain.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Integer(types[Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	var failed bool
	allocations := testing.AllocsPerRun(1000, func() {
		out, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		failed = failed || err != nil || out.Type() != plain
	})
	if failed || allocations != 0 {
		t.Fatal("unconstrained hot path", failed, allocations)
	}
}
