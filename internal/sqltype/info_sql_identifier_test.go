package sqltype

import (
	"context"
	"strings"
	"testing"
)

func TestInfoSQLIdentifierBuiltinDescriptor(t *testing.T) {
	types := testTypes()
	d, base := types[13248], types[Name]
	if d == nil || !d.Concrete() || !d.IsDomain() || !d.IsBuiltin() || d.IsUserDefined() || d.NamespaceName() != "information_schema" || d.Name() != "sql_identifier" || d.SQLName() != "information_schema.sql_identifier" || d.DatabaseName() != "NAME" || d.ID() == base.ID() {
		t.Fatal("selected builtin identity", d)
	}
	u := d.Usage()
	if !u.Valid() || u.Typmod != -1 || u.Collation.ID != 950 || u.BaseUsage().Type != base || u.BaseUsage().Typmod != -1 || u.BaseUsage().Collation.ID != 950 || d.DomainBase().Type != base || d.BaseType() != base {
		t.Fatal("logical/base usage", u)
	}
	a := types[d.AssociatedArray()]
	if a == nil || a.ID() != 13247 || a.Element() != d || a.Concrete() || a.Usage().Valid() || a.NamespaceName() != d.NamespaceName() {
		t.Fatal("selected implicit array became executable", a)
	}
	for _, id := range []ID{13246} {
		if sibling := types[id]; sibling.Concrete() || sibling.Usage().Valid() || !sibling.IsDomain() {
			t.Fatal("sibling promotion", id)
		}
	}
	n := Null(d)
	physicalNull, err := n.BaseValue()
	if err != nil || n.Type() != d || !n.IsNull() || physicalNull.Type() != base || !physicalNull.IsNull() {
		t.Fatal("domain NULL tag", n, physicalNull, err)
	}
	ctx := context.Background()
	for _, tc := range []struct{ input, output string }{
		{"", ""}, {"MiXeD", "MiXeD"}, {"O'Brien\\tail\n", "O'Brien\\tail\n"},
		{strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{strings.Repeat("a", 61) + "éZ", strings.Repeat("a", 61) + "é"},
		{strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
		{strings.Repeat("a", 60) + "🙂", strings.Repeat("a", 60)},
		{strings.Repeat("a", 59) + "🙂Z", strings.Repeat("a", 59) + "🙂"},
		{strings.Repeat("é", 32), strings.Repeat("é", 31)},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		physical, err := v.BaseValue()
		if err != nil || v.Type() != d || physical.Type() != base || v.text != physical.text || v.null != physical.null {
			t.Fatal("inline physical payload", v, physical, err)
		}
		wrapped, err := d.Wrap(physical)
		if err != nil || wrapped.Type() != d || wrapped.text != v.text {
			t.Fatal("domain wrap", wrapped, err)
		}
		out, err := d.Output(ctx, wrapped)
		if err != nil || out != tc.output {
			t.Fatal(out, tc.output, err)
		}
		literal, err := d.SQLLiteral(ctx, wrapped)
		baseLiteral, baseErr := base.SQLLiteral(ctx, physical)
		if err != nil || baseErr != nil || literal != baseLiteral {
			t.Fatal("base serialization", literal, baseLiteral, err, baseErr)
		}
	}
	for _, prefix := range []string{"", strings.Repeat("x", 63)} {
		for _, suffix := range []string{"\x00", "\xff", "\xc3", "\xc3("} {
			if _, err := d.Input(ctx, prefix+suffix, -1); err == nil {
				t.Fatalf("invalid suffix admitted: %q", prefix+suffix)
			}
		}
	}
	fresh := testTypes()
	rebound, err := d.RebindDomain(fresh[Name].Usage(), "renamed.sql_identifier")
	if err != nil {
		t.Fatal(err)
	}
	if rebound == d || rebound.DomainBase().Type != fresh[Name] || rebound.DomainBase().Type == base || !rebound.IsBuiltin() || rebound.IsUserDefined() || rebound.ID() != d.ID() || rebound.AssociatedArray() != d.AssociatedArray() || rebound.NamespaceName() != d.NamespaceName() || rebound.SQLName() != "renamed.sql_identifier" || d.SQLName() != "information_schema.sql_identifier" || d.DomainBase().Type != base {
		t.Fatal("immutable rebind provenance", rebound)
	}
	if _, err := d.RebindDomain(Usage{}, "renamed.sql_identifier"); err == nil {
		t.Fatal("invalid rebind admitted")
	}
	if _, err := base.RebindDomain(base.Usage(), "renamed.base"); err == nil {
		t.Fatal("non-domain rebind admitted")
	}
}
