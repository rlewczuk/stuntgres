package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDomainLogicalIdentityAndInlineValues(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	inputs := map[ID]string{Bool: "true", Bytea: `\x0001ff`, Name: "label", Int2: "7", Int4: "7", Int8: "7", Text: " hi ", Oid: "42", Float4: "1.5", Float8: "1.5", NameArray: `{NULL,"NULL",alpha}`, BPChar: "abc", VarChar: "abc", Date: "2024-02-29", Time: "12:34:56", Timestamp: "2024-02-29 12:34:56", Timestamptz: "2024-02-29 12:34:56+00", Interval: "2 days", Timetz: "12:34:56+00", Numeric: "12.345", Regclass: "42", UUID: "00112233-4455-6677-8899-aabbccddeeff"}
	for id, input := range inputs {
		t.Run(types[id].Name(), func(t *testing.T) {
			base := types[id]
			d, err := Domain(20000+id, "sample", "public.sample", base.Usage())
			if err != nil {
				t.Fatal(err)
			}
			chain, err := Domain(30000+id, "chain", "public.chain", d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			if !d.IsDomain() || chain.DomainBase().Type != d || chain.BaseType() != base || chain.Usage().Typmod != -1 || chain.DatabaseName() != base.DatabaseName() {
				t.Fatal("logical/immediate/physical identity", d, chain)
			}
			physical, err := base.Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
			value, err := chain.Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
			unwrapped, err := value.BaseValue()
			if err != nil || value.Type() != chain || unwrapped.Type() != base || !SameDatum(physical, unwrapped) {
				t.Fatal("inline payload", value, unwrapped, err)
			}
			actual, err := chain.Output(ctx, value)
			expected, e2 := base.Output(ctx, physical)
			if err != nil || e2 != nil || actual != expected {
				t.Fatal(actual, expected, err, e2)
			}
			if Null(chain).Type() != chain || !Null(chain).IsNull() {
				t.Fatal("typed NULL lost identity")
			}
			wrapped, err := chain.Wrap(Null(base))
			if err != nil || wrapped.Type() != chain || !wrapped.IsNull() {
				t.Fatal(wrapped, err)
			}
			if _, err = chain.Wrap(value); err == nil {
				t.Fatal("logical value accepted as physical payload")
			}
			sameID, err := Domain(chain.ID(), "chain", "public.chain", d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PlanCoercion(chain.Usage(), base.Usage(), Implicit)
			if err != nil {
				t.Fatal(err)
			}
			out, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil || out.Type() != base || !SameDatum(out, physical) {
				t.Fatal(out, err)
			}
			foreign, err := sameID.Wrap(physical)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.Apply(ctx, foreign, InputEnvironment{}, OutputEnvironment{}); err == nil {
				t.Fatal("foreign same-ID generation admitted")
			}
			restore, err := PlanCoercion(base.Usage(), chain.Usage(), Assignment)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := restore.Apply(ctx, out, InputEnvironment{}, OutputEnvironment{})
			if err != nil || restored.Type() != chain {
				t.Fatal(restored, err)
			}
			if _, err = value.Retag(d); err == nil {
				t.Fatal("unrelated domain identity remapped")
			}
		})
	}
	tree, err := Domain(45000, "tree", "public.tree", types[PgNodeTree].Usage())
	if err != nil {
		t.Fatal(err)
	}
	if value, err := tree.Wrap(Null(types[PgNodeTree])); err != nil || value.Type() != tree || !value.IsNull() {
		t.Fatal(value, err)
	}
	for _, base := range []*Descriptor{types[Void], types[Unknown], types[Char]} {
		if _, err := Domain(45001, "invalid", "public.invalid", base.Usage()); err == nil {
			t.Fatal("excluded physical base", base)
		}
	}
	if _, err := Domain(0, "invalid", "public.invalid", types[Int4].Usage()); err == nil {
		t.Fatal("invalid logical identity")
	}
}

func TestDomainTypmodsCollationAndCancellation(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	varchar, _, err := types[VarChar].ResolveModifiers(3)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Domain(46000, "short", "public.short", varchar)
	if err != nil {
		t.Fatal(err)
	}
	wider, _, err := types[VarChar].ResolveModifiers(4)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Domain(d.ID(), "short", "public.short", wider)
	if err != nil {
		t.Fatal(err)
	}
	original, err := d.Input(ctx, "abc", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = original.Retag(changed); err == nil {
		t.Fatal("same-ID domain with different base constraints retagged")
	}
	if n, ok := d.Usage().Length(); !ok || n != 3 {
		t.Fatal(n, ok)
	}
	if d.Usage().Collation != varchar.Collation || d.Usage().BaseUsage() != varchar {
		t.Fatal("inherited collation/typmod")
	}
	if _, err = d.Input(ctx, "abcd", -1); err == nil {
		t.Fatal("domain input skipped base typmod")
	}
	source := String(types[Text], "abcd")
	for _, tc := range []struct {
		mode CoercionContext
		want string
		code string
	}{{Assignment, "", "22001"}, {Explicit, "abc", ""}} {
		plan, err := PlanCoercion(types[Text].Usage(), d.Usage(), tc.mode)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
		if tc.code != "" {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal("explicit domain coercion", err)
		}
		actual, err := d.Output(ctx, value)
		if err != nil || actual != tc.want || value.Type() != d {
			t.Fatal(actual, value, err)
		}
	}
	numeric, _, err := types[Numeric].ResolveModifiers(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Domain(46001, "amount", "public.amount", numeric)
	if err != nil {
		t.Fatal(err)
	}
	if p, s, ok := n.Usage().DecimalSize(); !ok || p != 5 || s != 2 {
		t.Fatal(p, s, ok)
	}
	value, err := n.Input(ctx, "12.345", -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := n.Output(ctx, value)
	if err != nil || text != "12.35" {
		t.Fatal(text, err)
	}
	invalid := d.Usage()
	invalid.Collation = CollationRef{}
	if invalid.Valid() {
		t.Fatal("missing character collation accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = n.Input(canceled, "12.345", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	array, err := Domain(46002, "names", "public.names", types[NameArray].Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = array.Input(canceled, "{"+strings.Repeat("a,", 100)+"b}", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
