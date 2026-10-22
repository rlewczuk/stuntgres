package sqltype

import (
	"context"
	"testing"
)

func TestDomainNullabilityAuthority(t *testing.T) {
	integer := testTypes()[Int4]
	parent, err := Domain(49300, "required", "public.required", integer.Usage(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := Domain(49301, "child", "public.child", parent.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if parent.HasDomainChecks() || child.HasDomainChecks() || !parent.OwnDomainNotNull() || child.OwnDomainNotNull() || !child.DomainNotNull() || !child.HasDomainConstraints() {
		t.Fatal("own/effective nullability conflated")
	}
	nullable, err := Domain(49302, "nullable", "public.nullable", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := child.RebindDomain(nullable.Usage(), child.Name(), "public.child")
	if err != nil || rebound.DomainNotNull() || !child.DomainNotNull() {
		t.Fatal("ancestor rebind mutated old authority", err)
	}
	rebound, err = parent.RebindDomain(nullable.Usage(), parent.Name(), "public.required")
	if err != nil || !rebound.OwnDomainNotNull() || !rebound.DomainNotNull() {
		t.Fatal("own nullability lost on rebind", err)
	}
	fresh, err := PlanCoercion(integer.Usage(), child.Usage(), Assignment)
	if err != nil || !fresh.ChecksDomain() {
		t.Fatal("fresh conversion lacks authority", err)
	}
	if _, err := fresh.Apply(context.Background(), Null(integer), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("fresh null bypassed evaluator")
	}
	value, err := child.Wrap(Null(integer))
	if err != nil {
		t.Fatal(err)
	}
	same, err := PlanCoercion(child.Usage(), child.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	out, err := same.Apply(context.Background(), value, InputEnvironment{}, OutputEnvironment{})
	if err != nil || !out.IsNull() || out.Type() != child || same.ChecksDomain() {
		t.Fatal("same-domain null bypass changed", err)
	}
	physical, err := Integer(integer, 42)
	if err != nil {
		t.Fatal(err)
	}
	tagged, err := parent.Wrap(physical)
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := tagged.BaseValue()
	if err != nil || !SameDatum(unwrapped, physical) {
		t.Fatal("inline payload changed", err)
	}
	array, err := Array(49303, "_required", parent)
	if err != nil {
		t.Fatal(err)
	}
	record, err := Composite(49304, "required_record", "public.required_record", 49305, []CompositeMember{{Name: "v", Attribute: 1, Usage: parent.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*Descriptor{array, record} {
		if !ContainsDomainConstraints(typ) {
			t.Fatal("nested nullability authority lost")
		}
	}
}
