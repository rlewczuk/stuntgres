package sqltype

import (
	"context"
	"testing"
)

func TestCommentInquiryCoercion(t *testing.T) {
	ts := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		from, to    ID
		input, want string
	}{
		{Regclass, Oid, "4294967295", "4294967295"},
		{Text, Name, "pg_class", "pg_class"},
		{Int2, Oid, "1", "1"}, {Int4, Oid, "1", "1"}, {Int8, Oid, "4294967295", "4294967295"},
	} {
		t.Run(tc.input+ts[tc.from].Name(), func(t *testing.T) {
			p, err := PlanCoercion(ts[tc.from].Usage(), ts[tc.to].Usage(), Implicit)
			if err != nil {
				t.Fatal(err)
			}
			v, err := ts[tc.from].Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			out, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
			if err != nil || out.Type() != ts[tc.to] {
				t.Fatal(out, err)
			}
			got, err := ts[tc.to].Output(ctx, out)
			if err != nil || got != tc.want {
				t.Fatal(got, tc.want, err)
			}
			null, err := p.Apply(ctx, Null(ts[tc.from]), InputEnvironment{}, OutputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != ts[tc.to] {
				t.Fatal(null, err)
			}
		})
	}
	for _, tc := range []struct{ from, to ID }{{Int8, Int4}, {Text, Oid}} {
		if _, err := PlanCoercion(ts[tc.from].Usage(), ts[tc.to].Usage(), Implicit); err == nil {
			t.Fatal("unexpected implicit/transitive coercion", tc)
		}
	}
}
