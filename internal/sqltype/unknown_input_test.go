package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestTypeDescribeUnknownInputTransport(t *testing.T) {
	ctx := context.Background()
	types, fresh := testTypes(), testTypes()
	for _, tc := range []struct {
		id          ID
		typmod      int32
		input, want string
	}{
		{Text, -1, " x ", " x "}, {VarChar, 8, " x ", " x "}, {BPChar, 8, "x", "x   "}, {Name, -1, "name", "name"},
	} {
		u := types[tc.id].Usage()
		u.Typmod = tc.typmod
		value, err := u.Type.Input(ctx, tc.input, u.Typmod)
		if err != nil {
			t.Fatal(err)
		}
		p, err := PlanUnknownInputTransport(u, types[Text].Usage())
		if err != nil {
			t.Fatal(err)
		}
		if !p.IsUnknownInputTransport() || p.ChecksDomain() || p.SessionDependent() {
			t.Fatal("wrong IO operation/properties", tc.id)
		}
		out, err := p.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		text, textErr := out.Text()
		if err != nil || textErr != nil || out.Type() != types[Text] || text != tc.want {
			t.Fatal("source output bytes changed", tc.id, text, tc.want, err, textErr)
		}
		if tc.id == BPChar {
			ordinary, err := PlanCoercion(u, types[Text].Usage(), Explicit)
			if err != nil {
				t.Fatal(err)
			}
			trimmed, err := ordinary.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := trimmed.Text()
			if got != "x" || ordinary.Equal(p) {
				t.Fatal("padding witness failed to distinguish ordinary TEXT cast", got)
			}
		}
		null, err := p.Apply(ctx, Null(u.Type), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !null.IsNull() || null.Type() != types[Text] {
			t.Fatal("typed NULL transport", err)
		}
		nextSource := fresh[tc.id].Usage()
		nextSource.Typmod = tc.typmod
		next, err := p.Rebind(nextSource, fresh[Text].Usage(), Explicit)
		if err != nil || !next.IsUnknownInputTransport() || next.Equal(p) {
			t.Fatal("IO generation rebind lost operation", err)
		}
		if _, err := p.Rebind(nextSource, fresh[Text].Usage(), Implicit); err == nil {
			t.Fatal("implicit context acquired UNKNOWN IO authority")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Apply(canceled, value, InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("ordinary cancellation lost", err)
		}
	}
	domain, err := Domain(900001, "td_string", "td_string", types[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainValue, err := domain.Input(ctx, " domain ", -1)
	if err != nil {
		t.Fatal(err)
	}
	domainPlan, err := PlanUnknownInputTransport(domain.Usage(), types[Text].Usage())
	if err != nil || domainPlan.ChecksDomain() {
		t.Fatal("domain downcast must retain nominal input without rechecking it", err)
	}
	domainOut, err := domainPlan.Apply(ctx, domainValue, InputEnvironment{}, OutputEnvironment{})
	domainText, textErr := domainOut.Text()
	if err != nil || textErr != nil || domainText != " domain " || domainOut.Type() != types[Text] || domainValue.Type() != domain {
		t.Fatal("domain IO changed source ownership/output", err, textErr)
	}
	for _, source := range []Usage{types[Int4].Usage(), types[Unknown].Usage(), {}} {
		if _, err := PlanUnknownInputTransport(source, types[Text].Usage()); err == nil {
			t.Fatal("non-string executable source admitted")
		}
	}
	if _, err := PlanUnknownInputTransport(types[Text].Usage(), types[VarChar].Usage()); err == nil {
		t.Fatal("non-TEXT transport destination admitted")
	}
	badTarget := types[Text].Usage()
	badTarget.Typmod = 4
	if _, err := PlanUnknownInputTransport(types[Text].Usage(), badTarget); err == nil {
		t.Fatal("noncanonical TEXT target admitted")
	}
}
