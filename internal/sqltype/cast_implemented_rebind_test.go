package sqltype

import (
	"context"
	"testing"
	"time"
)

func TestCastImplementedRebind(t *testing.T) {
	ctx := context.Background()
	old, fresh := testTypes(), testTypes()
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	ny, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	input := InputEnvironment{Zone: utc, TransactionStart: time.Date(2004, 7, 1, 12, 0, 0, 0, time.UTC)}
	output := OutputEnvironment{Zone: utc}
	for _, tc := range []struct {
		s, t         ID
		source, want string
	}{{Int4, Bytea, "42", `\x0000002a`}, {Bytea, Int4, `\xffffffff`, "-1"}, {JSONB, Int4, "2.5", "3"}, {Interval, Time, "-2 hours", "22:00:00"}, {1007, 1000, "[0:2]={1,NULL,0}", "[0:2]={t,NULL,f}"}, {Int4Range, Int4Multirange, "[1,3)", "{[1,3)}"}} {
		plan, e := PlanCoercion(old[tc.s].Usage(), old[tc.t].Usage(), Explicit)
		if e != nil {
			t.Fatal(e)
		}
		next, e := plan.Rebind(fresh[tc.s].Usage(), fresh[tc.t].Usage(), Explicit)
		if e != nil {
			t.Fatal(e)
		}
		if next.method != plan.method || next.SessionDependent() != plan.SessionDependent() || next.RequiresCatalogOutput() != plan.RequiresCatalogOutput() || next.FunctionCastShortcut() != plan.FunctionCastShortcut() {
			t.Fatal("selected operation/properties changed at rebind")
		}
		previous := castUnitValue(t, old[tc.s], tc.source, input)
		current := castUnitValue(t, fresh[tc.s], tc.source, input)
		a, e := plan.Apply(ctx, previous, input, output)
		if e != nil {
			t.Fatal(e)
		}
		b, e := next.Apply(ctx, current, input, output)
		if e != nil {
			t.Fatal(e)
		}
		if a.Type() != old[tc.t] || b.Type() != fresh[tc.t] {
			t.Fatal("generation not rebound")
		}
		castUnitOutput(t, a, output, tc.want)
		castUnitOutput(t, b, output, tc.want)
		if _, e = next.Apply(ctx, previous, input, output); e == nil {
			t.Fatal("old generation accepted by rebound operation")
		}
		if _, e = plan.Apply(ctx, current, input, output); e == nil {
			t.Fatal("fresh generation accepted by old operation")
		}
	}
	plan, err := PlanCoercion(old[Timestamp].Usage(), old[Timestamptz].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SessionDependent() {
		t.Fatal("civil-to-instant lost session dependency")
	}
	value := castUnitValue(t, old[Timestamp], "2004-07-01 12:00:00", input)
	for _, tc := range []struct {
		zone *Zone
		want string
	}{{utc, "2004-07-01 12:00:00+00"}, {ny, "2004-07-01 16:00:00+00"}, {utc, "2004-07-01 12:00:00+00"}} {
		frame := input
		frame.Zone = tc.zone
		got, e := plan.Apply(ctx, value, frame, output)
		if e != nil {
			t.Fatal(e)
		}
		castUnitOutput(t, got, output, tc.want)
	}
	clock, err := PlanCoercion(old[Time].Usage(), old[Timetz].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	timeValue := castUnitValue(t, old[Time], "12:00:00", input)
	for _, tc := range []struct {
		month time.Month
		want  string
	}{{time.July, "12:00:00-04"}, {time.January, "12:00:00-05"}} {
		frame := input
		frame.Zone = ny
		frame.TransactionStart = time.Date(2004, tc.month, 1, 12, 0, 0, 0, time.UTC)
		got, e := clock.Apply(ctx, timeValue, frame, output)
		if e != nil {
			t.Fatal(e)
		}
		castUnitOutput(t, got, output, tc.want)
	}
	for _, tc := range []struct {
		s, t    ID
		catalog bool
	}{{Regclass, Text, true}, {Regtype, Text, true}, {Int4, Text, false}, {Int4, Bytea, false}, {2210, 1009, true}} {
		p, e := PlanCoercion(old[tc.s].Usage(), old[tc.t].Usage(), Explicit)
		if e != nil {
			t.Fatal(e)
		}
		if p.RequiresCatalogOutput() != tc.catalog {
			t.Fatalf("catalog-output property %d->%d=%v", tc.s, tc.t, p.RequiresCatalogOutput())
		}
		next, e := p.Rebind(fresh[tc.s].Usage(), fresh[tc.t].Usage(), Explicit)
		if e != nil || next.RequiresCatalogOutput() != tc.catalog {
			t.Fatal("rebound catalog output", e)
		}
	}
}
