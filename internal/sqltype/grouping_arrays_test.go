package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestGroupingArrayStructureAndScalarClasses(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id    ID
		a, b  string
		equal bool
	}{
		{1007, "{1,2}", "{1,2}", true}, {1007, "{1,2}", "[0:1]={1,2}", false},
		{1007, "{1,2}", "{{1,2}}", false}, {1007, "{}", "{NULL}", false},
		{1007, "{NULL}", "{NULL}", true}, {1007, "{1,NULL}", "{NULL,1}", false},
		{1231, "{1.0,NULL,-0}", "{1.00,NULL,0.00}", true},
		{1022, "{NaN,-0}", "{NaN,0}", true},
		{1014, `{a,"a "}`, `{"a ",a}`, true},
		{1009, "{a}", "{a }", true},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			d := types[tc.id]
			a, err := d.Input(ctx, tc.a, -1)
			if err != nil {
				t.Fatal(err)
			}
			b, err := d.Input(ctx, tc.b, -1)
			if err != nil {
				t.Fatal(err)
			}
			g, err := BindGrouping(d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			equal, err := g.Equal(ctx, a, b)
			if err != nil || equal != tc.equal {
				t.Fatal(equal, err)
			}
			ah, err := g.Hash(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			bh, err := g.Hash(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			if equal && ah != bh {
				t.Fatal("equal arrays hash differently", ah, bh)
			}
			if equal, err := g.Equal(ctx, Null(d), a); err != nil || equal {
				t.Fatal(equal, err)
			}
			if equal, err := g.Equal(ctx, Null(d), Null(d)); err != nil || !equal {
				t.Fatal(equal, err)
			}
			comparison, err := BindComparison(d, d, d.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			if equal, err := comparison.EqualValues(ctx, a, b, InputEnvironment{}); err != nil || equal != tc.equal {
				t.Fatal("ordinary array equality", equal, err)
			}
			if _, err := BindOrdering(d.Usage()); err == nil {
				t.Fatal("ordinary array ordering expanded")
			}
		})
	}
}

func TestGroupingNestedDomainsAndOwnership(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	whole, err := Domain(61000, "group_whole", "public.group_whole", types[1231].Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Array(61001, "_group_whole", whole)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := Domain(61002, "group_nested", "public.group_nested", nested.Usage())
	if err != nil {
		t.Fatal(err)
	}
	a, err := outer.Input(ctx, `{"{1.0,NULL}",NULL}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := outer.Input(ctx, `{"{1.00,NULL}",NULL}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	g, err := BindGrouping(outer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	equal, err := g.Equal(ctx, a, b)
	if err != nil || !equal {
		t.Fatal(equal, err)
	}
	ah, err := g.Hash(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	bh, err := g.Hash(ctx, b)
	if err != nil || ah != bh {
		t.Fatal(ah, bh, err)
	}
	if prepared, err := g.PrepareKey(ctx, a); err != nil || prepared != a {
		t.Fatal("ordinary array allocated/copy-changed key", prepared, err)
	}
	foreign := testTypes()
	value, err := foreign[1231].Input(ctx, "{1}", -1)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := BindGrouping(types[1231].Usage())
	if _, err := base.Hash(ctx, value); err == nil {
		t.Fatal("foreign array generation accepted")
	}
	for _, operation := range []string{"hash", "equal"} {
		cancel := &groupingCancelContext{Context: ctx, at: 4}
		var err error
		if operation == "hash" {
			_, err = g.Hash(cancel, a)
		} else {
			_, err = g.Equal(cancel, a, b)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(operation, err, cancel.calls)
		}
	}
}

func TestGroupingEligibility(t *testing.T) {
	types := testTypes()
	_, err := BindGrouping(types[Void].Usage())
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "could not identify an equality operator for type void" || diagnostic.Hint != "" {
		t.Fatal(err)
	}
	if _, err := BindGrouping(types[PgNodeTree].Usage()); err != nil {
		t.Fatal("represented binary-compatible tree equality rejected", err)
	}
}
