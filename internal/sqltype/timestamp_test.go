package sqltype

import (
	"context"
	"errors"
	"fmt"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

var timestampInputs = []struct{ input, output string }{
	{"4714-11-23 250000 BC", "4714-11-24 01:00:00 BC"},
	{"J .1", "4714-11-24 02:24:00 BC"},
	{"J .", "4714-11-24 00:00:00 BC"},
	{"1999 January 04:05:06 8.5", "1999-01-08 04:05:06.5"},
	{"January 8 04:05:06 99.1", "0099-01-08 04:05:06.1"},
	{"1999-01-08 119999 PM", "1999-01-09 00:40:39"},
	{"PST dst January 8 1999", "1999-01-08 00:00:00"},
	{"EDT January 8 1999", "1999-01-08 00:00:00"},
	{"epoch BC PM Friday dst", "1970-01-01 00:00:00"},
	{"- InFiNiTy BC PM Friday dst", "-infinity"},
	{"1999 1 8.5", "1999-01-08 00:00:00.5"},
	{"1999 1.50 8", "1999-01-08 00:00:00.5"},
	{"1999 1.00 8", "1999-01-08 00:00:00"},
	{"1999 January 8.5", "1999-01-08 00:00:00.5"},
	{"1999 1 8.5 04:05:06", "1999-01-08 04:05:06"},
	{"1999 1 8.", "1999-01-08 00:00:00"},
	{"1999-01-08 250000", "1999-01-09 01:00:00"},
	{"1999-01-08 2500", "1999-01-09 01:00:00"},
	{"1999-01-08 250000-8", "1999-01-09 01:00:00"},
	{"1999-01-08 049900", "1999-01-08 05:39:00"},
	{"1999-01-08 040599", "1999-01-08 04:06:39"},
	{"1999-01-08 250000.5", "1999-01-09 01:00:00.5"},
	{"1999-12-31 250000", "2000-01-01 01:00:00"},
	{"J on 2451187", "1999-01-08 00:00:00"},
	{"J UTC 2451187", "1999-01-08 00:00:00"},
	{"J +02 2451187", "1999-01-08 00:00:00"},
	{"J Friday 2451187", "1999-01-08 00:00:00"},
	{"J BC 2451187", "1999-01-08 00:00:00"},
	{"J PM 2451187", "1999-01-08 12:00:00"},
	{"1999-01-08 T on 040506", "1999-01-08 04:05:06"},
	{"1999-01-08 T 040506-8", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 PST dst", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 dst PST", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 UTC dst", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 dst UTC", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 +02 dst", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 dst +02", "1999-01-08 04:05:06"},
	{"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08 ", "1999-01-08 00:00:00"},
	{"at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!", "1999-01-08 00:00:00"},
	{"1999-01-08!", "1999-01-08 00:00:00"},
	{"PST 1999-01-08", "1999-01-08 00:00:00"},
	{"1999-01-08 04:05:06 + 8", "1999-01-08 04:05:06"},
	{"+ InFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\t8", "1999-01-08 04:05:06"},
	{"+\tInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\n8", "1999-01-08 04:05:06"},
	{"+\nInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\r8", "1999-01-08 04:05:06"},
	{"+\rInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\f8", "1999-01-08 04:05:06"},
	{"+\fInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\u000b8", "1999-01-08 04:05:06"},
	{"+\u000bInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 - 8", "1999-01-08 04:05:06"},
	{"- InFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\t8", "1999-01-08 04:05:06"},
	{"-\tInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\n8", "1999-01-08 04:05:06"},
	{"-\nInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\r8", "1999-01-08 04:05:06"},
	{"-\rInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\f8", "1999-01-08 04:05:06"},
	{"-\fInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\u000b8", "1999-01-08 04:05:06"},
	{"-\u000bInFiNiTy", "-infinity"},
	{"January 8 04:05:06 120000 PST", "120000-01-08 04:05:06"},
	{"1999 01 8.1", "1999-01-08 00:00:00.1"},
	{"1999 January 8.1", "1999-01-08 00:00:00.1"},
	{"January 8 99.1", "0099-01-08 00:00:00.1"},
	{"on epoch", "1970-01-01 00:00:00"},
	{"epoch at", "1970-01-01 00:00:00"},
	{"on infinity", "infinity"},
	{"infinity at", "infinity"},
	{"on +InFiNiTy", "infinity"},
	{"+InFiNiTy at", "infinity"},
	{"on - InFiNiTy", "-infinity"},
	{"- InFiNiTy at", "-infinity"},
	{"1999 8 JANUARY", "1999-01-08 00:00:00"},
	{"1999 8 JaNuArY 04:05:06 WEDS", "1999-01-08 04:05:06"},
	{"1999-JaN-08", "1999-01-08 00:00:00"},
	{"1999-01-08 04:05:06 aMeRiCa/nEw_yOrK", "1999-01-08 04:05:06"},
	{"BC JaNuArY 1 0001", "0001-01-01 00:00:00 BC"},
	{"PM JaNuArY 8 1999 04:05:06", "1999-01-08 16:05:06"},
	{"On JaNuArY 8 1999 At 04:05:06 pM aD PsT", "1999-01-08 16:05:06"},
	{"0001-01-08 04:05:06 bC", "0001-01-08 04:05:06 BC"},
	{"JuLiAn2451187.1", "1999-01-08 02:24:00"},
	{"EpOcH", "1970-01-01 00:00:00"},
	{"1999-01-08 aLlBaLlS", "1999-01-08 00:00:00"},
	{"1999.01.08.", "1999-01-08 00:00:00"},
	{"1999-01--08", "1999-01-08 00:00:00"},
	{"1999/01//08", "1999-01-08 00:00:00"},
	{"1999.01..08", "1999-01-08 00:00:00"},
	{"8 January 1999", "1999-01-08 00:00:00"},
	{"1999-Jan08", "1999-01-08 00:00:00"},
	{"1999 1 January", "1999-01-01 00:00:00"},
	{"1999-01-08-", "1999-01-08 00:00:00"},
	{"1999/01/08/", "1999-01-08 00:00:00"},
	{"1999--01--08", "1999-01-08 00:00:00"},
	{"1999//01//08", "1999-01-08 00:00:00"},
	{"1999 8 January", "1999-01-08 00:00:00"},
	{"1999 31 January", "1999-01-31 00:00:00"},
	{"2004-10-19 10:23:54+15:59:59", "2004-10-19 10:23:54"},
	{"2004-10-19 10:23:54-15:59:59", "2004-10-19 10:23:54"},
	{"January 08-1999", "1999-01-08 00:00:00"},
	{"Jan 08/1999", "1999-01-08 00:00:00"},
	{"January 1999 08", "1999-01-08 00:00:00"},
	{"UTC 1999-01-08 04:05:06", "1999-01-08 04:05:06"},
	{"BC January 1 1", "0001-01-01 00:00:00 BC"},
	{"1999.366", "2000-01-01 00:00:00"},
	{"1999 366", "2000-01-01 00:00:00"},
	{"1999.366 04:05:06", "2000-01-01 04:05:06"},
	{"1999 366 04:05:06", "2000-01-01 04:05:06"},
	{"2000.366", "2000-12-31 00:00:00"},
	{"2000 366", "2000-12-31 00:00:00"},
	{"2000.366 04:05:06", "2000-12-31 04:05:06"},
	{"1999 01-08", "1999-01-08 00:00:00"},
	{"Friday 19990108 04:05:06", "1999-01-08 04:05:06"},
	{"BC January 1 0001", "0001-01-01 00:00:00 BC"},
	{"PM January 8 1999 04:05:06", "1999-01-08 16:05:06"},
	{"1999-01-08 Friday 04:05:06", "1999-01-08 04:05:06"},
	{"1999.008 Friday 04:05:06", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 weds", "1999-01-08 04:05:06"},
	{"J2451187.1", "1999-01-08 02:24:00"},
	{"J2451187.2", "1999-01-08 04:48:00"},
	{"J2451187.000001", "1999-01-08 00:00:00.0864"},
	{"J2451187.0", "1999-01-08 00:00:00"},
	{"J2451187.1 UTC", "1999-01-08 02:24:00"},
	{"J2451187.0 UTC", "1999-01-08 00:00:00"},
	{"J2451187 04:05:06", "1999-01-08 04:05:06"},
	{"04:05:06 J2451187", "1999-01-08 04:05:06"},
	{"J2451187-8", "1999-01-08 00:00:00"},
	{"1999-01-08 04:05:06.", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05.", "1999-01-08 00:04:05"},
	{"1999-01-08 allballs", "1999-01-08 00:00:00"},
	{"1999-01-08 04:05:06.1e2", "1999-01-08 04:05:06.1"},
	{"1999-01-08 04:05:06 e2d", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 AB0CD1", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 A0:00:60", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 FOO/BAR0", "1999-01-08 04:05:06"},
	{"1999.008 04:05:06", "1999-01-08 04:05:06"},
	{"1999.008 allballs", "1999-01-08 00:00:00"},
	{"1999-01-08 040506-8", "1999-01-08 04:05:06"},
	{"19990108T040506-8", "1999-01-08 04:05:06"},
	{"JD2451187", "1999-01-08 00:00:00"},
	{"1999-01-08 04:05:06 - 8", "1999-01-08 04:05:06"},
	{"                                                                                                                                1999-01-08", "1999-01-08 00:00:00"},
	{"1999-01-08", "1999-01-08 00:00:00"},
	{"January 8, 1999", "1999-01-08 00:00:00"},
	{"January 8 1999", "1999-01-08 00:00:00"},
	{"1999-Jan-08", "1999-01-08 00:00:00"},
	{"Jan-08-1999", "1999-01-08 00:00:00"},
	{"08-Jan-1999", "1999-01-08 00:00:00"},
	{"8 Jan 1999", "1999-01-08 00:00:00"},
	{"1/8/1999", "1999-01-08 00:00:00"},
	{"1-8-1999", "1999-01-08 00:00:00"},
	{"1.8.1999", "1999-01-08 00:00:00"},
	{"08-Jan-99", "1999-01-08 00:00:00"},
	{"Jan-08-99", "1999-01-08 00:00:00"},
	{"19990108", "1999-01-08 00:00:00"},
	{"990108", "1999-01-08 00:00:00"},
	{"1999.008", "1999-01-08 00:00:00"},
	{"1999 008", "1999-01-08 00:00:00"},
	{"J2451187", "1999-01-08 00:00:00"},
	{"1/18/1999", "1999-01-18 00:00:00"},
	{"01/02/03", "2003-01-02 00:00:00"},
	{"01/02/69", "2069-01-02 00:00:00"},
	{"01/02/70", "1970-01-02 00:00:00"},
	{"0099-01-08", "0099-01-08 00:00:00"},
	{"J2451187.5", "1999-01-08 12:00:00"},
	{"1999-01-08 04:05:06", "1999-01-08 04:05:06"},
	{"1999-01-08T04:05:06", "1999-01-08 04:05:06"},
	{"19990108T040506", "1999-01-08 04:05:06"},
	{"990108 040506", "1999-01-08 04:05:06"},
	{"January 8 04:05:06 1999 PST", "1999-01-08 04:05:06"},
	{"Friday January 8 1999 at 04:05:06", "1999-01-08 04:05:06"},
	{"on Fri 8 January 1999 04:05:06", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05", "1999-01-08 04:05:00"},
	{"1999-01-08 0405", "1999-01-08 04:05:00"},
	{"1999-01-08 040506.789", "1999-01-08 04:05:06.789"},
	{"1999-01-08 04:05 AM", "1999-01-08 04:05:00"},
	{"1999-01-08 04:05 PM", "1999-01-08 16:05:00"},
	{"1999-01-08 12:00 AM", "1999-01-08 00:00:00"},
	{"1999-01-08 12:00 PM", "1999-01-08 12:00:00"},
	{"1999-01-08 04:05:06.1", "1999-01-08 04:05:06.1"},
	{"1999-01-08 04:05:06.1234564", "1999-01-08 04:05:06.123456"},
	{"1999-01-08 04:05:06.1234566", "1999-01-08 04:05:06.123457"},
	{"1999-01-08 04:05:06-8", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06-800", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06-08:00", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06-8:00:00", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06+0730", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06+07:30:00", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06+02", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06Z", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 zulu", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 UTC", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 GMT", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 PST", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 EDT", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 MSK", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 America/New_York", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 aMeRiCa/NeW_yOrK", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 US/Eastern", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 Etc/UTC", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 PST8PDT", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 FOOBAR0", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 FOO-2:30BAR", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 FOO+2BAR3", "1999-01-08 04:05:06"},
	{"January 8, 99 BC 04:05:06", "0099-01-08 04:05:06 BC"},
	{"0001-01-01 00:00:00 BC", "0001-01-01 00:00:00 BC"},
	{"1999-01-08 04:05:06 AD PST", "1999-01-08 04:05:06"},
	{"1999-01-08 04:05:06 PST BC", "1999-01-08 04:05:06 BC"},
	{"2000-02-29 24:00:00", "2000-03-01 00:00:00"},
	{"1999-12-31 23:59:60", "2000-01-01 00:00:00"},
	{"2024-03-10 02:30 America/New_York", "2024-03-10 02:30:00"},
	{"2024-11-03 01:30 America/New_York", "2024-11-03 01:30:00"},
	{"epoch", "1970-01-01 00:00:00"},
	{"INFINITY", "infinity"},
	{"-Infinity", "-infinity"},
	{"4714-11-24 BC", "4714-11-24 00:00:00 BC"},
	{"294276-12-31 23:59:59.999999", "294276-12-31 23:59:59.999999"},
}

func TestTimestampDocumentedInputs(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	for _, tc := range timestampInputs {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.output {
				t.Fatalf("got %q (%v), want %q", out, err, tc.output)
			}
			again, err := d.Input(ctx, out, -1)
			if err != nil || again != v {
				t.Fatal("canonical reparse", again, err)
			}
			if _, err = v.Integer(); err == nil {
				t.Fatal("timestamp leaked into integer accessor")
			}
		})
	}
}
func TestTimestampPrecisionAndComparison(t *testing.T) {
	types := testTypes()
	d := types[Timestamp]
	ctx := context.Background()
	for p, out := range []string{"2000-01-01 00:00:00", "2000-01-01 00:00:00.1", "2000-01-01 00:00:00.12", "2000-01-01 00:00:00.123", "2000-01-01 00:00:00.1235", "2000-01-01 00:00:00.12346", "2000-01-01 00:00:00.123456"} {
		u, w, e := d.ResolveModifiers(int32(p))
		if e != nil || w != nil || u.Typmod != int32(p) {
			t.Fatal(u, w, e)
		}
		decl, e := u.Declaration()
		if e != nil || decl != fmt.Sprintf("TIMESTAMP(%d)", p) {
			t.Fatal(decl, e)
		}
		v, e := d.Input(ctx, "2000-01-01 00:00:00.123456", -1)
		if e != nil {
			t.Fatal(e)
		}
		plan, e := PlanCoercion(d.Usage(), u, Assignment)
		if e != nil {
			t.Fatal(e)
		}
		v, e = plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		got, _ := d.Output(ctx, v)
		if e != nil || got != out {
			t.Fatal(p, got, e)
		}
		for _, v := range []Value{Null(d), {typ: d, integer: TimestampPositiveInfinity}, {typ: d, integer: TimestampNegativeInfinity}} {
			got, e := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
			if e != nil || got != v {
				t.Fatal(got, e)
			}
		}
	}
	for _, tc := range []struct {
		input, out string
		p          int32
	}{
		{"2000-01-01 00:00:00.5", "2000-01-01 00:00:01", 0},
		{"1999-12-31 23:59:59.5", "1999-12-31 23:59:59", 0},
		{"1999-12-31 23:59:59.9996", "2000-01-01 00:00:00", 3},
		{"2000-01-01 00:00:00.0000005", "2000-01-01 00:00:00", 6},
		{"2000-01-01 00:00:00.0000015", "2000-01-01 00:00:00.000002", 6},
		{"294276-12-31 23:59:59.999999", "294277-01-01 00:00:00", 0},
	} {
		v, e := d.Input(ctx, tc.input, tc.p)
		out, _ := d.Output(ctx, v)
		if e != nil || out != tc.out {
			t.Fatal(tc, out, e)
		}
	}
	v, _ := d.Input(ctx, "2000-01-01 00:00:00.1245", -1)
	u3, _, _ := d.ResolveModifiers(3)
	u2, _, _ := d.ResolveModifiers(2)
	p3, _ := PlanCoercion(d.Usage(), u3, Assignment)
	p2, _ := PlanCoercion(u3, u2, Assignment)
	v, _ = p3.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
	v, _ = p2.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
	out, _ := d.Output(ctx, v)
	if out != "2000-01-01 00:00:00.13" {
		t.Fatal(out)
	}
	cmp, _ := BindComparison(d, d, CollationRef{}, false)
	prev := Value{}
	for i, s := range []string{"-infinity", "0001-01-01 BC", "epoch", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-01-01 00:00:00.000001", "infinity"} {
		v, e := d.Input(ctx, s, -1)
		if e != nil {
			t.Fatal(e)
		}
		if i > 0 {
			n, e := cmp.Compare(ctx, prev, v, InputEnvironment{})
			if e != nil || n != -1 {
				t.Fatal(n, e)
			}
		}
		n, e := cmp.Compare(ctx, v, v, InputEnvironment{})
		if e != nil || n != 0 {
			t.Fatal(n, e)
		}
		prev = v
	}
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"1999.366", "2000-01-01", 0},
		{"1999.366", "1999.365", 1},
	} {
		left, err := d.Input(ctx, tc.left, -1)
		if err != nil {
			t.Fatal(err)
		}
		right, err := d.Input(ctx, tc.right, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := cmp.Compare(ctx, left, right, InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	eq, _ := BindComparisonPredicate(CompareEqual, d, d, types[Bool], CollationRef{}, false)
	v, e := eq.Evaluate(ctx, Null(d), prev, InputEnvironment{})
	if e != nil || !v.IsNull() || v.Type() != types[Bool] {
		t.Fatal(v, e)
	}
	if _, e := TimestampValue(types[Int8], 0); e == nil {
		t.Fatal("foreign constructor")
	}
	if _, e := TimestampValue(d, math.MaxInt64-1); e == nil {
		t.Fatal("out of range constructor")
	}
	u, w, e := d.ResolveModifiers(7)
	want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6"}
	if e != nil || u.Typmod != 6 || w == nil || *w != want {
		t.Fatal(u, w, e)
	}
	_, _, e = d.ResolveModifiers(-1)
	var diagnostic *sqlstate.Error
	if !errors.As(e, &diagnostic) || diagnostic.Message != "TIMESTAMP(-1) precision must not be negative" || diagnostic.Code != "22023" {
		t.Fatal(e)
	}
}
func TestTimestampTimeFractionConversionAllocations(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	for _, tc := range []struct {
		name, text string
		raw, want  time.Time
	}{
		{"midpoint-down", "2000-01-01 00:00:00.0001255", time.Date(2000, 1, 1, 0, 0, 0, 125500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 125000, time.UTC)},
		{"midpoint-up", "2000-01-01 00:00:00.0001265", time.Date(2000, 1, 1, 0, 0, 0, 126500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 127000, time.UTC)},
		{"ordinary-even", "2000-01-01 00:00:00.0000005", time.Date(2000, 1, 1, 0, 0, 0, 500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"ordinary-odd", "2000-01-01 00:00:00.0000015", time.Date(2000, 1, 1, 0, 0, 0, 1500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 2000, time.UTC)},
		{"zero", "2000-01-01 00:00:00", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"year-carry", "1999-12-31 23:59:59.9999995", time.Date(1999, 12, 31, 23, 59, 59, 999999500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"negative-epoch-down", "1999-12-31 23:59:59.0001255", time.Date(1999, 12, 31, 23, 59, 59, 125500, time.UTC), time.Date(1999, 12, 31, 23, 59, 59, 125000, time.UTC)},
		{"negative-epoch-up", "1999-12-31 23:59:59.0001265", time.Date(1999, 12, 31, 23, 59, 59, 126500, time.UTC), time.Date(1999, 12, 31, 23, 59, 59, 127000, time.UTC)},
		{"bc-max-offset", "0001-10-19 10:23:54.0001255+15:59:59 BC", time.Date(0, 10, 19, 10, 23, 54, 125500, time.FixedZone("east", 57599)), time.Date(0, 10, 19, 10, 23, 54, 125000, time.UTC)},
		{"bc-min-offset", "0001-10-19 10:23:54.0001265-15:59:59 BC", time.Date(0, 10, 19, 10, 23, 54, 126500, time.FixedZone("west", -57599)), time.Date(0, 10, 19, 10, 23, 54, 127000, time.UTC)},
		{"finite-min", "4714-11-24 00:00:00.0001255 BC", time.Date(-4713, 11, 24, 0, 0, 0, 125500, time.UTC), time.Date(-4713, 11, 24, 0, 0, 0, 125000, time.UTC)},
		{"finite-last", "294276-12-31 23:59:59.999999499", time.Date(294276, 12, 31, 23, 59, 59, 999999499, time.UTC), time.Date(294276, 12, 31, 23, 59, 59, 999999000, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, precision := range []int32{-1, 6} {
				text, err := d.Input(ctx, tc.text, precision)
				if err != nil {
					t.Fatal(err)
				}
				got, err := text.TimestampTime()
				if err != nil || got != tc.want {
					t.Fatal(precision, got, tc.want, err)
				}
			}
			var value Value
			var err error
			allocations := testing.AllocsPerRun(1000, func() { value, err = TimestampFromTime(d, tc.raw) })
			if err != nil || value.Type() != d || value.IsNull() {
				t.Fatal(value, err)
			}
			got, err := value.TimestampTime()
			if err != nil || got != tc.want {
				t.Fatal(got, tc.want, err)
			}
			if allocations != 0 {
				t.Fatalf("civil fractional input allocated %g times/run, want 0", allocations)
			}
		})
	}
}

func TestTimestampEnvironmentAndCivilTime(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	env := InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 23, 59, 59, 123456000, time.UTC)}
	for _, tc := range []struct{ s, out string }{{"now", "2024-12-31 23:59:59.123456"}, {"today", "2024-12-31 00:00:00"}, {"today+2", "2024-12-31 00:00:00"}, {"tomorrow", "2025-01-01 00:00:00"}, {"yesterday", "2024-12-30 00:00:00"}, {"today 04:05:06 UTC", "2024-12-31 04:05:06"}, {"04:05:06 tomorrow", "2025-01-01 04:05:06"}, {"yesterday 040506-8", "2024-12-30 04:05:06"}} {
		v, e := d.InputWithEnvironment(ctx, tc.s, -1, env)
		out, _ := d.Output(ctx, v)
		if e != nil || out != tc.out {
			t.Fatal(out, e)
		}
	}
	if _, e := d.Input(ctx, "now", -1); e == nil {
		t.Fatal("missing environment accepted")
	}
	for _, year := range []int{0, -98, 2004, 12000, 294276} {
		raw := time.Date(year, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200))
		v, e := TimestampFromTime(d, raw)
		if e != nil {
			t.Fatal(e)
		}
		out, e := v.TimestampTime()
		_, offset := out.Zone()
		if e != nil || out.Year() != year || out.Hour() != 10 || out.Nanosecond() != 123457000 || offset != 0 {
			t.Fatal(out, e)
		}
	}
	zones.once.Do(initializeZones)
	for name := range zones.members {
		if !recognizedTimestampZone(name, InputEnvironment{}) {
			t.Fatal(name)
		}
	}
	for name := range zones.abbreviations[TimezoneAbbreviationsDefault] {
		v, e := d.Input(ctx, "1999-01-08 04:05:06 "+name, -1)
		out, _ := d.Output(ctx, v)
		if e != nil || out != "1999-01-08 04:05:06" {
			t.Fatal(name, out, e)
		}
	}
}

func TestTimestampFractionGrammar(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  float64
	}{
		{".", 0}, {".0", 0}, {".1", 0.1}, {".123456", 0.123456},
	} {
		got, status := fraction(tc.input)
		if status != timestampDecoded || got != tc.want {
			t.Fatal(tc, got, status)
		}
	}
	for _, input := range []string{"", "..", ".1.2", ".1e2", ".nonsense"} {
		if _, status := fraction(input); status != timestampMalformed {
			t.Fatal(input, status)
		}
	}
	// The compact provider accepts the fraction grammar, but ParseDateTime
	// classifies a bare compact trailing dot as DTK_DATE, rejected by full input.
	p := timestampDecoder{mask: dateComplete}
	if status := p.compact("040506."); status != timestampDecoded ||
		!p.timeSet || p.h != 4 || p.min != 5 || p.s != 6 || p.u != 0 {
		t.Fatal(p, status)
	}
}

func TestTimestampInputAllocations(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	env := InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)}
	for _, tc := range []struct {
		input string
		nanos int
	}{
		{"1999-01-08 04:05:06", 0},
		{"1999-01-08 04:05:06.123456", 123456000},
		{"1999-01-08 04:05:06+02:30", 0},
		{"1999-01-08 04:05:06 - 8", 0},
		{"1999-01-08 04:05:06 +\t\n\r\f\v8", 0},
		{"1999-01-08- 04:05:06", 0},
		{"1999--01--08 04:05:06", 0},
		{"1999 8 January 04:05:06", 0},
		{"1999 8 JANUARY 04:05:06", 0},
		{"1999-JaN-08 04:05:06", 0},
		{"1999 8 JaNuArY 04:05:06 WEDS", 0},
		{"1999-01-08 04:05:06 aMeRiCa/nEw_yOrK", 0},
		{"1999-01-08 04:05:06 PsT", 0},
		{"1999-01-08 04:05:06 aM", 0},
		{"1999-01-08 04:05:06 e2", 0},
		{"1999-01-08 04:05:06.1e2", 100000000},
		{"1999.01.08. 04:05:06", 0},
		{"1999.01..08 04:05:06", 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var got Value
			var err error
			allocations := testing.AllocsPerRun(1000, func() {
				got, err = d.InputWithEnvironment(ctx, tc.input, -1, env)
			})
			if err != nil || got.Type() != d || got.IsNull() {
				t.Fatal(got, err)
			}
			tm, err := got.TimestampTime()
			if err != nil || tm != time.Date(1999, 1, 8, 4, 5, 6, tc.nanos, time.UTC) {
				t.Fatal(tm, err)
			}
			if allocations != 0 {
				t.Fatalf("successful codec input allocated %g times/run, want 0", allocations)
			}
		})
	}
	for _, tc := range []struct{ input, want string }{
		{"1999-01-08 04:05:06 pM", "1999-01-08 16:05:06"},
		{"1999-01-08 04:05:06 aD", "1999-01-08 04:05:06"},
		{"0001-01-08 04:05:06 bC", "0001-01-08 04:05:06 BC"},
		{"JuLiAn2451187.1", "1999-01-08 02:24:00"},
		{"On JaNuArY 8 1999 At 04:05:06", "1999-01-08 04:05:06"},
		{"1999-01-08 aLlBaLlS", "1999-01-08 00:00:00"},
		{"EpOcH", "1970-01-01 00:00:00"},
		{"NoW", "2024-12-31 23:59:59"},
		{"ToDaY+2", "2024-12-31 00:00:00"},
		{"ToMoRrOw", "2025-01-01 00:00:00"},
		{"YeStErDaY", "2024-12-30 00:00:00"},
		{"+InFiNiTy", "infinity"},
		{"-InFiNiTy", "-infinity"},
		{"- InFiNiTy", "-infinity"},
		{"+\t\n\r\f\vInFiNiTy", "infinity"},
		{"on - InFiNiTy at", "-infinity"},
		{"on EpOcH at", "1970-01-01 00:00:00"},
		{"January 8 04:05:06 120000 PST", "120000-01-08 04:05:06"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var got Value
			var err error
			allocations := testing.AllocsPerRun(1000, func() {
				got, err = d.InputWithEnvironment(ctx, tc.input, -1, env)
			})
			if err != nil || got.Type() != d || got.IsNull() {
				t.Fatal(got, err)
			}
			out, err := d.Output(ctx, got)
			if err != nil || out != tc.want {
				t.Fatal(out, err)
			}
			if allocations != 0 {
				t.Fatalf("successful codec input allocated %g times/run, want 0", allocations)
			}
		})
	}
	for _, tc := range []struct {
		name        string
		input, want time.Time
	}{
		{"civil-time-utc", time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.UTC), time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)},
		{"civil-time-bc-offset", time.Date(0, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200)), time.Date(0, 10, 19, 10, 23, 54, 123457000, time.UTC)},
		{"civil-time-max-offset", time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 57599)), time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)},
		{"civil-time-min-offset", time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("west", -57599)), time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Value
			var err error
			allocations := testing.AllocsPerRun(1000, func() {
				got, err = TimestampFromTime(d, tc.input)
			})
			if err != nil || got.Type() != d || got.IsNull() {
				t.Fatal(got, err)
			}
			tm, err := got.TimestampTime()
			if err != nil || tm != tc.want {
				t.Fatal(tm, err)
			}
			if allocations != 0 {
				t.Fatalf("successful civil time import allocated %g times/run, want 0", allocations)
			}
		})
	}
}

func BenchmarkTimestampInput(b *testing.B) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	env := InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)}
	for _, input := range []string{
		"1999-01-08 04:05:06", "1999-01-08 04:05:06.123456", "1999-01-08 04:05:06+02:30",
		"1999 8 January 04:05:06", "1999 8 JaNuArY 04:05:06 WEDS",
		"1999-01-08 04:05:06 aMeRiCa/nEw_yOrK", "1999-01-08 04:05:06 PsT",
		"On JaNuArY 8 1999 At 04:05:06", "JuLiAn2451187.1", "NoW",
		"1999-01-08 04:05:06 - 8", "- InFiNiTy", "on EpOcH at",
		"1999 1 8.5", "1999-01-08 250000.5", "J on UTC 2451187",
		"1999-01-08 T on 040506", "1999-01-08 04:05:06 PsT DsT",
	} {
		b.Run(input, func(b *testing.B) {
			var got Value
			var err error
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				got, err = d.InputWithEnvironment(ctx, input, -1, env)
			}
			b.StopTimer()
			if err != nil || got.Type() != d || got.IsNull() {
				b.Fatal(got, err)
			}
		})
	}
}

func TestTimestampDateDelimiterSpans(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	for _, input := range []string{"1999--01--08", "1999//01//08", "1999..01..08"} {
		v, err := d.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(input, err)
		}
		got, err := v.TimestampTime()
		if err != nil || got != time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC) {
			t.Fatal(input, got, err)
		}
	}
	if status := (&timestampDecoder{}).date("1999-01-08-09"); status != timestampMalformed {
		t.Fatal("extra date component", status)
	}
}

func TestTimestampCoreDateWords(t *testing.T) {
	// REL_18_0's core table, independent of the zone vocabulary.
	for _, word := range []string{
		"+infinity", "-infinity", "ad", "allballs", "am", "apr", "april", "at",
		"aug", "august", "bc", "d", "dec", "december", "dow", "doy", "dst", "epoch",
		"feb", "february", "fri", "friday", "h", "infinity", "isodow", "isoyear",
		"j", "jan", "january", "jd", "jul", "julian", "july", "jun", "june",
		"m", "mar", "march", "may", "mm", "mon", "monday", "nov", "november",
		"now", "oct", "october", "on", "pm", "s", "sat", "saturday", "sep",
		"sept", "september", "sun", "sunday", "t", "thu", "thur", "thurs",
		"thursday", "today", "tomorrow", "tue", "tues", "tuesday", "wed",
		"wednesday", "weds", "y", "yesterday",
	} {
		if !coreDateWord(word) {
			t.Fatalf("core table word %q treated as zone prefix", word)
		}
	}
	for _, word := range []string{"current", "invalid", "zulu", "e2", "nowhere", "e", "foobar", "pst", "nonsense"} {
		if coreDateWord(word) {
			t.Fatalf("non-core word %q treated as core prefix", word)
		}
	}
}

func TestTimestampCoreSpecialPrefixTokens(t *testing.T) {
	for _, word := range []string{"now", "today", "tomorrow", "yesterday", "epoch", "infinity", "allballs", "NOW", "ToDaY", "AllBalls"} {
		for _, suffix := range []struct {
			text string
			kind byte
		}{{"1", 'n'}, {"+2", 'z'}} {
			input := word + suffix.text
			fields, n, ok := tokenizeTimestamp(input)
			if !ok || n != 2 || fields[0].text != word || fields[0].kind != 's' ||
				fields[1].text != suffix.text || fields[1].kind != suffix.kind {
				t.Fatalf("%q tokens %#v count %d valid %v", input, fields, n, ok)
			}
		}
	}
}
