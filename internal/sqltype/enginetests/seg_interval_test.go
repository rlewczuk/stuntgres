package enginetests

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func segIntervalTypes(t *testing.T) (*sqltype.Descriptor, *sqltype.Descriptor) {
	t.Helper()
	d, err := sqltype.Seg(65000, "seg", "public.seg")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range sqltype.Builtins() {
		if b.ID() == sqltype.Bool {
			return d, b
		}
	}
	t.Fatal("missing bool")
	return nil, nil
}
func segIntervalDatum(t *testing.T, d *sqltype.Descriptor, lower, upper uint32, ld, ud, lm, um uint8) sqltype.Value {
	t.Helper()
	v, err := sqltype.SegValue(d, sqltype.SegDatum{LowerBits: lower, UpperBits: upper, LowerDigits: ld, UpperDigits: ud, LowerMarker: lm, UpperMarker: um})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSegIntervalKernel(t *testing.T) {
	d, boolean := segIntervalTypes(t)
	// Raw IEEE-754 words and all expected predicates are independent of seg I/O.
	cases := []struct {
		a, b [2]uint32
		want [8]bool
	}{
		{[2]uint32{0, 0x3f800000}, [2]uint32{0x40000000, 0x40400000}, [8]bool{true, false, true, false, false, false, false, false}},
		{[2]uint32{0, 0x3f800000}, [2]uint32{0x3f800000, 0x40000000}, [8]bool{false, false, true, false, false, true, false, false}},
		{[2]uint32{0, 0x40000000}, [2]uint32{0x3f800000, 0x3f800000}, [8]bool{false, false, false, false, false, true, true, false}},
		{[2]uint32{0x3f800000, 0xbf800000}, [2]uint32{0xc0000000, 0}, [8]bool{false, true, true, true, false, true, false, true}},
		{[2]uint32{0x3f800000, 0xbf800000}, [2]uint32{0x3f800000, 0xbf800000}, [8]bool{true, true, true, true, true, false, true, true}},
		{[2]uint32{0x3f800000, 0xbf800000}, [2]uint32{0, 0}, [8]bool{true, true, true, true, false, false, false, true}},
		{[2]uint32{1, 1}, [2]uint32{2, 2}, [8]bool{true, false, true, false, false, false, false, false}},
		{[2]uint32{0x3f800000, 0x3f800000}, [2]uint32{0x3f800001, 0x3f800001}, [8]bool{true, false, true, false, false, false, false, false}},
		{[2]uint32{0, 0x7f800000}, [2]uint32{0x3f800000, 0x3f800000}, [8]bool{false, false, false, false, false, true, true, false}},
	}
	for _, tc := range cases {
		values := [2]sqltype.Value{segIntervalDatum(t, d, tc.a[0], tc.a[1], 1, 1, 0, 0), segIntervalDatum(t, d, tc.b[0], tc.b[1], 1, 1, 0, 0)}
		for i, want := range tc.want {
			p, err := sqltype.BindSegInterval(sqltype.SegIntervalOperation(i), boolean.Usage(), d.Usage(), d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Evaluate(t.Context(), values)
			got, conversion := v.Boolean()
			if err != nil || conversion != nil || v.Type() != boolean || got != want {
				t.Fatal(tc, i, got, err, conversion)
			}
			if a, b := util.AllocationsPerCall(64, func() {
				if _, err := p.Evaluate(context.Background(), values); err != nil {
					panic(err)
				}
			}); a != 0 || b != 0 {
				t.Fatal("kernel allocated", a, b)
			}
		}
	}
	foreign, _ := sqltype.Seg(65001, "seg", "public.seg")
	p, _ := sqltype.BindSegInterval(sqltype.SegSame, boolean.Usage(), d.Usage(), d.Usage())
	for _, values := range [][2]sqltype.Value{{sqltype.Null(foreign), sqltype.Null(d)}, {sqltype.Value{}, sqltype.Null(d)}} {
		if _, err := p.Evaluate(t.Context(), values); err == nil {
			t.Fatal("NULL hid foreign operand")
		}
	}
	for _, op := range []sqltype.SegIntervalOperation{0, 255} {
		if _, err := sqltype.BindSegInterval(op, d.Usage(), d.Usage(), d.Usage()); err == nil {
			t.Fatal("invalid result")
		}
	}
	if _, err := sqltype.BindSegInterval(255, boolean.Usage(), d.Usage(), d.Usage()); err == nil {
		t.Fatal("invalid operation")
	}
	if _, err := (sqltype.SegIntervalPlan{}).Evaluate(t.Context(), [2]sqltype.Value{}); err == nil {
		t.Fatal("zero plan")
	}
}

func TestSegIntervalDatumIdentity(t *testing.T) {
	d, boolean := segIntervalTypes(t)
	for op := sqltype.CompareEqual; op <= sqltype.CompareGreaterEqual; op++ {
		if _, err := sqltype.BindComparisonPredicate(op, d, d, boolean, sqltype.CollationRef{}, false); err == nil {
			t.Fatal("selected predicate granted generic comparison", op)
		}
	}
	if _, err := sqltype.BindOrdering(d.Usage()); err == nil {
		t.Fatal("selected predicate granted generic ordering")
	}
	if _, err := sqltype.BindGrouping(d.Usage()); err == nil {
		t.Fatal("selected predicate granted grouping")
	}
	p, _ := sqltype.BindSegInterval(sqltype.SegSame, boolean.Usage(), d.Usage(), d.Usage())
	negative := segIntervalDatum(t, d, 0x80000000, 0x80000000, 2, 2, 0, 0)
	positive := segIntervalDatum(t, d, 0, 0, 2, 2, 0, 0)
	v, err := p.Evaluate(t.Context(), [2]sqltype.Value{negative, positive})
	got, _ := v.Boolean()
	if err != nil || !got || sqltype.SameDatum(negative, positive) {
		t.Fatal("numeric zero versus representation", err)
	}
	if same, err := negative.SamePayload(t.Context(), positive); err != nil || same {
		t.Fatal("raw payload identity", same, err)
	}
	base := segIntervalDatum(t, d, math.Float32bits(1), math.Float32bits(1), 2, 2, 0, 0)
	for _, field := range []int{0, 1, 2, 3} {
		raw, _ := base.SegDatum()
		switch field {
		case 0:
			raw.LowerDigits = 3
		case 1:
			raw.UpperDigits = 3
		case 2:
			raw.LowerMarker = '~'
		case 3:
			raw.UpperMarker = '>'
		}
		other, err := sqltype.SegValue(d, raw)
		if err != nil {
			t.Fatal(err)
		}
		for op := sqltype.SegLeft; op <= sqltype.SegContained; op++ {
			plan, _ := sqltype.BindSegInterval(op, boolean.Usage(), d.Usage(), d.Usage())
			value, err := plan.Evaluate(t.Context(), [2]sqltype.Value{base, other})
			actual, _ := value.Boolean()
			want := op != sqltype.SegLeft && op != sqltype.SegRight && op != sqltype.SegSame
			if err != nil || actual != want {
				t.Fatal("metadata predicate", field, op, actual, err)
			}
		}
		if field == 1 {
			a, _ := d.Output(t.Context(), base)
			b, _ := d.Output(t.Context(), other)
			if a != b {
				t.Fatal("hidden precision witness", a, b)
			}
		}
	}
}

func TestSegIntervalCancellation(t *testing.T) {
	d, boolean := segIntervalTypes(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	expired, stop := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer stop()
	for op := sqltype.SegLeft; op <= sqltype.SegContained; op++ {
		p, _ := sqltype.BindSegInterval(op, boolean.Usage(), d.Usage(), d.Usage())
		if v, err := p.Evaluate(ctx, [2]sqltype.Value{sqltype.Null(d), sqltype.Null(d)}); !errors.Is(err, context.Canceled) || v.Type() != nil {
			t.Fatal("cancellation before NULL", err)
		}
		if v, err := p.Evaluate(expired, [2]sqltype.Value{sqltype.Null(d), sqltype.Null(d)}); !errors.Is(err, context.DeadlineExceeded) || v.Type() != nil {
			t.Fatal("deadline before NULL", err)
		}
		if v, err := p.Evaluate(t.Context(), [2]sqltype.Value{sqltype.Null(d), sqltype.Null(d)}); err != nil || !v.IsNull() || v.Type() != boolean {
			t.Fatal("strict typed NULL", err)
		}
	}
	<-expired.Done()
	if !errors.Is(expired.Err(), context.DeadlineExceeded) {
		t.Fatal("deadline source", expired.Err())
	}
}
