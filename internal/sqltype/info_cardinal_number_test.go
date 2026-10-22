package sqltype

import (
	"context"
	"testing"
)

// This observer checks the generic coercion boundary, not nonnegative semantics.
// Engine units and reference SQL exercise the actual bound VALUE expression.
type infoCardinalNumberInputObserver struct {
	t            *testing.T
	target, base *Descriptor
	values       []Value
}

func (o *infoCardinalNumberInputObserver) CheckDomain(_ context.Context, target *Descriptor, value Value) error {
	o.t.Helper()
	if target != o.target || value.Type() != o.base {
		o.t.Fatal("foreign conversion authority", target, value.Type())
	}
	o.values = append(o.values, value)
	return nil
}

func TestInfoCardinalNumberBuiltinDescriptor(t *testing.T) {
	types := testTypes()
	d, base := types[13243], types[Int4]
	if d == nil || !d.Concrete() || !d.IsDomain() || !d.IsBuiltin() || d.IsUserDefined() || !d.HasDomainChecks() || !d.ownDomainChecks || d.NamespaceName() != "information_schema" || d.Name() != "cardinal_number" || d.SQLName() != "information_schema.cardinal_number" || d.DatabaseName() != "INT4" || d.BaseType() != base || d.ID() == base.ID() {
		t.Fatal("selected builtin identity", d)
	}
	u := d.Usage()
	if !u.Valid() || u.Typmod != -1 || u.Collation != (CollationRef{}) || u.BaseUsage() != base.Usage() || d.DomainBase() != base.Usage() {
		t.Fatal("logical/base usage", u, u.BaseUsage())
	}
	if n, ok := u.Length(); ok || n != 0 {
		t.Fatal("integer length metadata", n, ok)
	}
	if p, s, ok := u.DecimalSize(); ok || p != 0 || s != 0 {
		t.Fatal("integer decimal metadata", p, s, ok)
	}
	facts, ok := d.PGTypeMetadata()
	want := PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{Length: 4, ByValue: true, Alignment: "i", Storage: "p"}, Kind: "d", Category: "N", Base: Int4, Input: 2597, Output: 43, Receive: 2598, Send: 2407, Modifier: -1}
	if !ok || facts != want || d.PostgreSQLTypeLength() != 4 {
		t.Fatal("domain catalog metadata", facts, ok)
	}
	a := types[d.AssociatedArray()]
	if a == nil || a.ID() != 13242 || a.Element() != d || a.Concrete() || a.Usage().Valid() || a.NamespaceName() != d.NamespaceName() {
		t.Fatal("selected companion authority", a)
	}
	if sibling := types[13246]; sibling.Concrete() || sibling.Usage().Valid() || !sibling.IsDomain() {
		t.Fatal("character_data promotion", sibling)
	}
	ctx := context.Background()
	observer := &infoCardinalNumberInputObserver{t: t, target: d, base: base}
	input := InputEnvironment{DomainChecks: observer}
	plan, err := PlanCoercion(base.Usage(), u, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	source, err := Integer(base, 7)
	if err != nil {
		t.Fatal(err)
	}
	value, err := plan.Apply(ctx, source, input, OutputEnvironment{})
	if err != nil || value.Type() != d || value.integer != 7 || source.Type() != base || source.integer != 7 {
		t.Fatal("inline payload/source immutability", value, source, err)
	}
	unwrapped, err := value.BaseValue()
	if err != nil || unwrapped != source || len(observer.values) != 1 || observer.values[0] != source {
		t.Fatal("CHECK base payload", unwrapped, observer.values, err)
	}
	out, err := d.Output(ctx, value)
	if err != nil || out != "7" {
		t.Fatal("base output", out, err)
	}
	literal, err := d.SQLLiteral(ctx, value)
	baseLiteral, baseErr := base.SQLLiteral(ctx, source)
	if err != nil || baseErr != nil || literal != baseLiteral {
		t.Fatal("base serialization", literal, baseLiteral, err, baseErr)
	}
	null, err := plan.Apply(ctx, Null(base), input, OutputEnvironment{})
	if err != nil || null.Type() != d || !null.IsNull() || len(observer.values) != 2 || observer.values[1].Type() != base || !observer.values[1].IsNull() {
		t.Fatal("typed NULL CHECK boundary", null, observer.values, err)
	}
	fresh := testTypes()
	rebound, err := d.RebindDomain(fresh[Int4].Usage(), d.Name(), "information_schema.cardinal_number")
	if err != nil {
		t.Fatal(err)
	}
	if rebound == d || rebound.DomainBase().Type != fresh[Int4] || rebound.DomainBase().Type == base || !rebound.IsBuiltin() || rebound.IsUserDefined() || !rebound.HasDomainChecks() || !rebound.ownDomainChecks || rebound.ID() != 13243 || rebound.AssociatedArray() != 13242 || rebound.NamespaceName() != "information_schema" || d.DomainBase() != base.Usage() {
		t.Fatal("immutable rebind provenance", rebound)
	}
	retagged, err := value.Retag(rebound)
	if err != nil || retagged.Type() != rebound || retagged.integer != 7 || value.Type() != d {
		t.Fatal("inline generation reownership", retagged, value, err)
	}
}
