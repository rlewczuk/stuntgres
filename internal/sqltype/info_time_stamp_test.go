package sqltype

import (
	"context"
	"testing"
)

func TestInfoTimeStampBuiltinDescriptor(t *testing.T) {
	types := testTypes()
	d := types[13254]
	base := types[Timestamptz]
	if d == nil || !d.Concrete() || !d.IsDomain() || !d.IsBuiltin() || d.IsUserDefined() || d.NamespaceName() != "information_schema" || d.Name() != "time_stamp" || d.SQLName() != "information_schema.time_stamp" || d.DatabaseName() != "TIMESTAMPTZ" || d.ID() == base.ID() {
		t.Fatal("selected builtin identity", d)
	}
	u := d.Usage()
	if !u.Valid() || u.Typmod != -1 || u.Collation != (CollationRef{}) || u.BaseUsage().Type != base || u.BaseUsage().Typmod != 2 || d.DomainBase() != u.BaseUsage() {
		t.Fatal("logical/base usage", u)
	}
	a := types[d.AssociatedArray()]
	if a == nil || a.ID() != 13253 || a.Element() != d || a.Concrete() || a.Usage().Valid() || a.NamespaceName() != d.NamespaceName() {
		t.Fatal("selected implicit array became executable", a)
	}
	for _, id := range []ID{13243, 13246, 13256} {
		if sibling := types[id]; sibling.Concrete() || sibling.Usage().Valid() || !sibling.IsDomain() {
			t.Fatal("sibling promotion", id)
		}
	}
	if n := Null(d); n.Type() != d || !n.IsNull() {
		t.Fatal("domain NULL tag", n)
	}
	for _, tc := range []struct{ input, output string }{
		{"2004-10-19 10:23:54.126+02", "2004-10-19 08:23:54.13+00"},
		{"2004-10-19 23:59:59.999+00", "2004-10-20 00:00:00+00"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		physical, err := v.BaseValue()
		if err != nil || v.Type() != d || physical.Type() != base || v.integer != physical.integer || v.null != physical.null {
			t.Fatal("inline physical payload", v, physical, err)
		}
		out, err := d.Output(context.Background(), v)
		if err != nil || out != tc.output {
			t.Fatal(out, err)
		}
	}
	fresh := testTypes()
	freshBase := fresh[Timestamptz].Usage()
	freshBase.Typmod = 2
	rebound, err := d.RebindDomain(freshBase, "renamed.time_stamp")
	if err != nil {
		t.Fatal(err)
	}
	if rebound == d || rebound.DomainBase().Type != fresh[Timestamptz] || rebound.DomainBase().Type == base || !rebound.IsBuiltin() || rebound.IsUserDefined() || rebound.ID() != d.ID() || rebound.AssociatedArray() != d.AssociatedArray() || rebound.NamespaceName() != d.NamespaceName() || rebound.SQLName() != "renamed.time_stamp" || d.SQLName() != "information_schema.time_stamp" {
		t.Fatal("immutable rebind provenance", rebound)
	}
	if _, err := d.RebindDomain(Usage{}, "renamed.time_stamp"); err == nil {
		t.Fatal("invalid rebind admitted")
	}
	if _, err := base.RebindDomain(base.Usage(), "renamed.base"); err == nil {
		t.Fatal("non-domain rebind admitted")
	}
}
