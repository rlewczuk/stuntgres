package sqltype

import (
	"context"
	"testing"
)

// This observer checks the generic coercion boundary, not YES/NO semantics.
// The actual CHECK programme is exercised by engine units and reference SQL.
type infoYesOrNoInputObserver struct {
	t            *testing.T
	target, base *Descriptor
	values       []Value
}

func (o *infoYesOrNoInputObserver) CheckDomain(_ context.Context, target *Descriptor, value Value) error {
	o.t.Helper()
	if target != o.target || value.Type() != o.base {
		o.t.Fatal("foreign conversion authority", target, value.Type())
	}
	o.values = append(o.values, value)
	return nil
}

func TestInfoYesOrNoBuiltinDescriptor(t *testing.T) {
	types := testTypes()
	d, base := types[13256], types[VarChar]
	if d == nil || !d.Concrete() || !d.IsDomain() || !d.IsBuiltin() || d.IsUserDefined() || !d.HasDomainChecks() || !d.ownDomainChecks || d.NamespaceName() != "information_schema" || d.Name() != "yes_or_no" || d.SQLName() != "information_schema.yes_or_no" || d.DatabaseName() != "VARCHAR" || d.BaseType() != base || d.ID() == base.ID() {
		t.Fatal("selected builtin identity", d)
	}
	u := d.Usage()
	physical := Usage{Type: base, Typmod: 7, Collation: CollationRef{ID: 950, Derivation: Default}}
	if !u.Valid() || u.Typmod != -1 || u.Collation != physical.Collation || u.BaseUsage() != physical || d.DomainBase() != physical {
		t.Fatal("logical/base usage", u, u.BaseUsage())
	}
	if n, ok := u.Length(); !ok || n != 3 || d.PostgreSQLTypeLength() != -1 {
		t.Fatal("physical metadata", n, ok, d.PostgreSQLTypeLength())
	}
	a := types[d.AssociatedArray()]
	if a == nil || a.ID() != 13255 || a.Element() != d || a.Concrete() || a.Usage().Valid() || a.NamespaceName() != d.NamespaceName() {
		t.Fatal("selected companion authority", a)
	}
	if n := Null(d); n.Type() != d || !n.IsNull() {
		t.Fatal("domain NULL tag", n)
	}
	ctx := context.Background()
	observer := &infoYesOrNoInputObserver{t: t, target: d, base: base}
	input := InputEnvironment{DomainChecks: observer}
	plan, err := PlanCoercion(base.Usage(), u, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	source := String(base, "YES   ")
	value, err := plan.Apply(ctx, source, input, OutputEnvironment{})
	if err != nil || value.Type() != d || value.text != "YES" || source.text != "YES   " || source.Type() != base {
		t.Fatal("sized inline payload/source immutability", value, source, err)
	}
	unwrapped, err := value.BaseValue()
	if err != nil || unwrapped.Type() != base || unwrapped.text != value.text || unwrapped.null != value.null {
		t.Fatal("inline base payload", unwrapped, err)
	}
	if len(observer.values) != 1 || observer.values[0] != unwrapped {
		t.Fatal("CHECK did not receive physically sized input", observer.values)
	}
	null, err := plan.Apply(ctx, Null(base), input, OutputEnvironment{})
	if err != nil || null.Type() != d || !null.IsNull() || len(observer.values) != 2 || !observer.values[1].IsNull() {
		t.Fatal("fresh typed NULL conversion", null, observer.values, err)
	}
	if _, err := plan.Apply(ctx, String(base, "YESx"), input, OutputEnvironment{}); err == nil || len(observer.values) != 2 {
		t.Fatal("typmod must precede CHECK", err, observer.values)
	}
	for _, format := range []struct{ input, output string }{{"YES", "YES"}, {"NO", "NO"}} {
		v, err := d.InputWithEnvironment(ctx, format.input, -1, input)
		if err != nil || v.Type() != d {
			t.Fatal(v, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != format.output {
			t.Fatal(out, err)
		}
	}
	fresh := testTypes()
	freshBase := physical
	freshBase.Type = fresh[VarChar]
	rebound, err := d.RebindDomain(freshBase, d.Name(), "information_schema.yes_or_no")
	if err != nil {
		t.Fatal(err)
	}
	if rebound == d || rebound.DomainBase() != freshBase || rebound.DomainBase().Type == base || !rebound.IsBuiltin() || rebound.IsUserDefined() || !rebound.HasDomainChecks() || !rebound.ownDomainChecks || rebound.ID() != 13256 || rebound.AssociatedArray() != 13255 || rebound.NamespaceName() != "information_schema" || d.DomainBase() != physical {
		t.Fatal("immutable rebind provenance", rebound)
	}
	retagged, err := value.Retag(rebound)
	if err != nil || retagged.Type() != rebound || retagged.text != value.text || value.Type() != d {
		t.Fatal("inline generation reownership", retagged, value, err)
	}
}
