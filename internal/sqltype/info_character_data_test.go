package sqltype

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestInfoCharacterDataBuiltinDescriptor(t *testing.T) {
	types := testTypes()
	d, base := types[13246], types[VarChar]
	if d == nil || !d.Concrete() || !d.IsDomain() || !d.IsBuiltin() || d.IsUserDefined() || d.HasDomainConstraints() || d.ownDomainChecks || d.OwnDomainNotNull() || d.NamespaceName() != "information_schema" || d.Name() != "character_data" || d.SQLName() != "information_schema.character_data" || d.DatabaseName() != "VARCHAR" || d.BaseType() != base || d.ID() == base.ID() {
		t.Fatal("selected builtin identity", d)
	}
	u := d.Usage()
	physical := Usage{Type: base, Typmod: -1, Collation: CollationRef{ID: 950, Derivation: Default}}
	if !u.Valid() || u.Typmod != -1 || u.Collation != physical.Collation || u.BaseUsage() != physical || d.DomainBase() != physical {
		t.Fatal("logical/base usage", u, u.BaseUsage())
	}
	if n, ok := u.Length(); !ok || n != math.MaxInt64 || d.PostgreSQLTypeLength() != -1 {
		t.Fatal("unlimited physical metadata", n, ok)
	}
	a := types[d.AssociatedArray()]
	if a == nil || a.ID() != 13245 || a.Element() != d || a.Concrete() || a.Usage().Valid() || a.NamespaceName() != d.NamespaceName() {
		t.Fatal("lookup-only companion authority", a)
	}
	ctx := context.Background()
	for _, text := range []string{"ordinary", "", "tail  ", "é🙂", strings.Repeat("x", 80)} {
		v, err := d.Input(ctx, text, -1)
		if err != nil || v.Type() != d || v.text != text || v.IsNull() {
			t.Fatal("domain input", v, err)
		}
		source := String(base, text)
		wrapped, err := d.Wrap(source)
		if err != nil || wrapped != v || source.Type() != base || source.text != text {
			t.Fatal("inline wrap/source immutability", wrapped, source, err)
		}
		unwrapped, err := v.BaseValue()
		if err != nil || unwrapped != source || v.Type() != d {
			t.Fatal("inline unwrap/source immutability", unwrapped, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != text {
			t.Fatal("domain output", out, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		baseLiteral, err := base.SQLLiteral(ctx, source)
		if err != nil || literal != baseLiteral {
			t.Fatal("base serialization", literal, baseLiteral, err)
		}
		for _, mode := range []CoercionContext{Assignment, Explicit} {
			plan, err := PlanCoercion(base.Usage(), u, mode)
			if err != nil {
				t.Fatal(err)
			}
			converted, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
			if err != nil || converted != v || source != String(base, text) {
				t.Fatal("unconstrained inline conversion", converted, source, err)
			}
			null, err := plan.Apply(ctx, Null(base), InputEnvironment{}, OutputEnvironment{})
			if err != nil || null != Null(d) {
				t.Fatal("typed NULL conversion", null, err)
			}
		}
	}
	null := Null(d)
	physicalNull, err := null.BaseValue()
	if err != nil || !physicalNull.IsNull() || physicalNull.Type() != base || null.Type() != d {
		t.Fatal("inline typed NULL", null, physicalNull, err)
	}
	literal, err := d.SQLLiteral(ctx, null)
	if err != nil || literal != "NULL" {
		t.Fatal(literal, err)
	}
	for _, bad := range []string{"a\x00b", string([]byte{0xff})} {
		if _, err := d.Input(ctx, bad, -1); err == nil {
			t.Fatal("invalid physical input accepted", []byte(bad))
		}
	}
	freshBase := physical
	freshBase.Type = testTypes()[VarChar]
	rebound, err := d.RebindDomain(freshBase, d.Name(), d.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	if rebound == d || rebound.DomainBase() != freshBase || rebound.BaseType() == base || !rebound.IsBuiltin() || rebound.IsUserDefined() || rebound.HasDomainConstraints() || rebound.ownDomainChecks || rebound.OwnDomainNotNull() || rebound.ID() != 13246 || rebound.AssociatedArray() != 13245 || rebound.NamespaceName() != d.NamespaceName() || rebound.SQLName() != d.SQLName() || rebound.Usage().Collation != physical.Collation || d.DomainBase() != physical {
		t.Fatal("immutable rebind provenance", rebound)
	}
}
