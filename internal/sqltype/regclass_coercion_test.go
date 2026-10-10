package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

type propertyRegclassResolver struct {
	calls int
	err   error
}

func (r *propertyRegclassResolver) ResolveRegclass(ctx context.Context, name string) (uint32, error) {
	r.calls++
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if r.err != nil {
		return 0, r.err
	}
	if name != "owned_index" {
		return 0, errors.New("unexpected regclass name")
	}
	return 1234, nil
}

func TestRegclassInquiryCoercions(t *testing.T) {
	ts := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		source ID
		input  string
		want   uint32
	}{
		{Oid, "0", 0}, {Oid, "4294967295", 4294967295},
		{Int2, "-1", 4294967295}, {Int4, "-1", 4294967295},
		{Int8, "0", 0}, {Int8, "4294967295", 4294967295},
	} {
		plan, err := PlanCoercion(ts[tc.source].Usage(), ts[Regclass].Usage(), Implicit)
		if err != nil || plan.SessionDependent() {
			t.Fatal(tc, plan, err)
		}
		input, err := ts[tc.source].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		back, err := PlanCoercion(ts[Regclass].Usage(), ts[Oid].Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := back.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := identity.OID()
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		null, err := plan.Apply(ctx, Null(ts[tc.source]), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !null.IsNull() || null.Type() != ts[Regclass] {
			t.Fatal(tc, null, err)
		}
		foreign, err := plan.Apply(ctx, Null(testTypes()[tc.source]), InputEnvironment{}, OutputEnvironment{})
		if err == nil {
			t.Fatal("accepted foreign descriptor", foreign)
		}
	}
	for _, input := range []string{"-1", "4294967296"} {
		value, _ := ts[Int8].Input(ctx, input, -1)
		plan, _ := PlanCoercion(ts[Int8].Usage(), ts[Regclass].Usage(), Implicit)
		_, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "OID out of range" {
			t.Fatal(err)
		}
	}
	for _, source := range []ID{Bool, Numeric, Name, BPChar} {
		if _, err := PlanCoercion(ts[source].Usage(), ts[Regclass].Usage(), Implicit); err == nil {
			t.Fatal("unrequested implicit cast", source)
		}
	}
	for _, source := range []ID{Text, VarChar} {
		plan, err := PlanCoercion(ts[source].Usage(), ts[Regclass].Usage(), Implicit)
		if err != nil || !plan.SessionDependent() {
			t.Fatal(source, err)
		}
		resolver := &propertyRegclassResolver{}
		value, err := plan.Apply(ctx, String(ts[source], "owned_index"), InputEnvironment{Regclass: resolver}, OutputEnvironment{})
		if err != nil || value.Type() != ts[Regclass] || resolver.calls != 1 {
			t.Fatal(value, resolver.calls, err)
		}
		back, _ := PlanCoercion(ts[Regclass].Usage(), ts[Oid].Usage(), Implicit)
		identity, err := back.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		got, accessErr := identity.OID()
		if err != nil || accessErr != nil || got != 1234 {
			t.Fatal(got, err, accessErr)
		}
		missing := errors.New("missing relation")
		resolver.err = missing
		if _, err := plan.Apply(ctx, String(ts[source], "owned_index"), InputEnvironment{Regclass: resolver}, OutputEnvironment{}); !errors.Is(err, missing) {
			t.Fatal(err)
		}
	}
	domain, err := Domain(90001, "inquiry_oid", "inquiry_oid", ts[Oid].Usage())
	if err != nil {
		t.Fatal(err)
	}
	base, _ := OIDValue(ts[Oid], 403)
	value, err := domain.Wrap(base)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCoercion(domain.Usage(), ts[Regclass].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	out, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
	if err != nil || out.Type() != ts[Regclass] {
		t.Fatal(out, err)
	}
	for _, target := range []ID{Oid, Int4, Int8} {
		plan, err := PlanCoercion(ts[Regclass].Usage(), ts[target].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Apply(ctx, String(ts[Regclass], "4294967295"), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if target == Oid {
			n, err := value.OID()
			if err != nil || n != 4294967295 {
				t.Fatal(n, err)
			}
		} else {
			n, err := value.Integer()
			want := int64(4294967295)
			if target == Int4 {
				want = -1
			}
			if err != nil || n != want {
				t.Fatal(n, err)
			}
		}
		null, err := plan.Apply(ctx, Null(ts[Regclass]), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !null.IsNull() || null.Type() != ts[target] {
			t.Fatal(null, err)
		}
	}
}
