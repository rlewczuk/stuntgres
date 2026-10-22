package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestExprCollationComparisonAuthority(t *testing.T) {
	types := testTypes()
	text, boolean := types[Text], types[Bool]
	ctx := context.Background()
	ref, err := RegisteredCollation(88001, 6411)
	if err != nil {
		t.Fatal(err)
	}
	other, err := RegisteredCollation(88002, 6411)
	if err != nil {
		t.Fatal(err)
	}
	p, err := BindComparisonPredicate(CompareEqual, text, text, boolean, ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if selected, conflict := p.InputCollation(); selected != ref || conflict {
		t.Fatal(selected, conflict)
	}
	u := text.Usage()
	u.Collation = ref
	if !p.EqualityFor(u) {
		t.Fatal("matching authority excluded from hash eligibility")
	}
	u.Collation = other
	if p.EqualityFor(u) {
		t.Fatal("equivalent providers erased object identity")
	}
	for _, pair := range [][2]string{{"A", "a"}, {"é", "é"}, {"ß", "SS"}} {
		v, err := p.Evaluate(ctx, String(text, pair[0]), String(text, pair[1]), InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := v.Boolean()
		if err != nil || truth {
			t.Fatal("case or normalization changed deterministic equality", pair, v, err)
		}
	}
	ambiguous, err := BindComparisonPredicate(CompareEqual, text, text, boolean, ref, true)
	if err != nil {
		t.Fatal(err)
	}
	u.Collation = ref
	if ambiguous.EqualityFor(u) {
		t.Fatal("hash optimization erased demanded conflict")
	}
	_, err = ambiguous.Evaluate(ctx, String(text, "A"), String(text, "A"), InputEnvironment{})
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
	if !errors.As(err, &d) || *d != want {
		t.Fatal("equal-byte shortcut erased demand", err)
	}
	for _, pair := range [][2]Value{{Null(text), String(text, "A")}, {String(text, "A"), Null(text)}, {Null(text), Null(text)}} {
		v, err := ambiguous.Evaluate(ctx, pair[0], pair[1], InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != boolean {
			t.Fatal(v, err)
		}
	}
	g, err := ambiguous.EqualityGrouping()
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Hash(ctx, String(text, "A"))
	want.Message = "could not determine which collation to use for string hashing"
	if !errors.As(err, &d) || *d != want {
		t.Fatal("membership must demand its actual hash provider", err)
	}
	if _, err := g.Hash(ctx, Null(text)); err != nil {
		t.Fatal("NULL hash demanded collation", err)
	}
	for _, invalid := range []CollationRef{{}, {ID: 88001}, {ID: 88002, provider: ref.provider, registeredID: 88001}} {
		_, err := BindComparison(text, text, invalid, false)
		if err == nil || errors.As(err, &d) {
			t.Fatal("invalid capability admitted or fabricated SQL diagnostic", invalid, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ambiguous.Evaluate(canceled, Null(text), Null(text), InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Evaluate(canceled, String(text, "A"), String(text, "A"), InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := g.Hash(canceled, Null(text)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExprCollationKeyAuthority(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	ref, err := RegisteredCollation(88101, 950)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []*Descriptor{types[Text], types[BPChar]} {
		u := base.Usage()
		u.Collation = ref
		domain, err := Domain(88102+base.ID(), "key_domain", "key_domain", u)
		if err != nil {
			t.Fatal(err)
		}
		for _, usage := range []Usage{u, domain.Usage()} {
			order, err := BindOrdering(usage)
			if err != nil {
				t.Fatal(err)
			}
			if got, conflict := order.InputCollation(); got != ref || conflict {
				t.Fatal("ordering lost capability", got, conflict)
			}
			group, err := BindGrouping(usage)
			if err != nil {
				t.Fatal(err)
			}
			if group.usage.Collation != ref || group.comparison.inputCollation != ref {
				t.Fatal("grouping lost capability")
			}
			class, ok := DefaultIndexClass(usage.Type)
			if !ok {
				t.Fatal("missing Btree family")
			}
			btree, err := bindTestIndexOrdering(usage, class)
			if err != nil || btree.comparison.inputCollation != ref {
				t.Fatal(btree, err)
			}
			hashClass, ok := DefaultHashIndexClassID(usage.Type.BaseType().ID())
			if !ok {
				t.Fatal("missing hash family")
			}
			hash, err := bindTestIndexHash(usage, hashClass)
			if err != nil || hash.grouping.usage.Collation != ref {
				t.Fatal(hash, err)
			}
			a, err := usage.Type.Input(ctx, "A", usage.Typmod)
			if err != nil {
				t.Fatal(err)
			}
			second := "A"
			if base.ID() == BPChar {
				second = "A  "
			}
			b, err := usage.Type.Input(ctx, second, usage.Typmod)
			if err != nil {
				t.Fatal(err)
			}
			if a.Type() != usage.Type || b.Type() != usage.Type {
				t.Fatal("domain import lost declared key identity")
			}
			if base.ID() == BPChar && b.text != "A  " {
				t.Fatal("BPCHAR witness lost physical trailing spaces")
			}
			equal, err := group.Equal(ctx, a, b)
			if err != nil || !equal {
				t.Fatal(equal, err)
			}
			ha, err := group.Hash(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			hb, err := group.Hash(ctx, b)
			if err != nil || ha != hb {
				t.Fatal("equal keys disagree on hash", ha, hb, err)
			}
			if sign, err := btree.Compare(ctx, a, b, InputEnvironment{}); err != nil || sign != 0 {
				t.Fatal("Btree lost BPCHAR normalization", sign, err)
			}
			bad := usage
			bad.Collation = CollationRef{ID: ref.ID}
			if _, err := BindOrdering(bad); err == nil {
				t.Fatal("invalid empty ordering admitted")
			}
			if _, err := BindGrouping(bad); err == nil {
				t.Fatal("invalid allNULL grouping admitted")
			}
			if _, err := bindTestIndexOrdering(bad, class); err == nil {
				t.Fatal("invalid Btree admitted")
			}
			if _, err := bindTestIndexHash(bad, hashClass); err == nil {
				t.Fatal("invalid hash index admitted")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := group.Equal(canceled, a, a); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := group.Hash(canceled, Null(usage.Type)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}
