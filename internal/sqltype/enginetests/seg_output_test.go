package enginetests

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

var segCases = []struct{ in, out string }{
	{"1", "1"}, {"-1.0", "-1.0"}, {"1.00", "1.00"}, {"1.00000", "1.00000"},
	{"1.000000", "1.00000"}, {"12.34567890123456", "12.3457"},
	{"1e7", "1e+07"}, {"1.0e7", "1.0e+07"}, {"1e-7", "1e-07"}, {"1.0e-7", "1.0e-07"},
	{"2e-6", "2e-06"}, {"2e-5", "2e-05"}, {"2e-4", "0.0002"}, {"2e-3", "0.002"}, {"2e-2", "0.02"}, {"2e-1", "0.2"},
	{"2e-0", "2"}, {"2e+0", "2"}, {"2e+1", "2e1"}, {"2e+2", "2e2"}, {"2e+3", "2e3"}, {"2e+4", "2e4"}, {"2e+5", "2e+05"}, {"2e+6", "2e+06"},
	{"0.000000120", "1.20e-07"}, {"3.400e5", "3.400e+05"},
	{"~ 6.5", "~6.5"}, {"< 6.5", "<6.5"}, {"> 6.5", ">6.5"},
	{"~1.5 .. 2.5", "~1.5 .. 2.5"}, {"1.5 .. ~2.5", "1.5 .. ~2.5"}, {"~1.5 .. ~2.5", "~1.5 .. ~2.5"},
	{"<1.5 .. >2.5", "<1.5 .. >2.5"}, {">1.5 .. <2.5", ">1.5 .. <2.5"}, {"1.5 .. >2.5", "1.5 .. >2.5"}, {"<1.5 .. 2.5", "<1.5 .. 2.5"},
	{"0..", "0 .."}, {"0...", "0 .."}, {"0 ..", "0 .."}, {"0 ...", "0 .."},
	{"..0", ".. 0"}, {"...0", ".. 0"}, {".. 0", ".. 0"}, {"... 0", ".. 0"},
	{"0 .. 1", "0 .. 1"}, {"-1 .. 0", "-1 .. 0"}, {"-1 .. 1", "-1 .. 1"}, {"6.25 .. 6.50", "6.25 .. 6.50"},
	{"1.5e-2 .. 2E-2", "0.015 .. 0.02"}, {"0(+-)1", "-1 .. 1"}, {"0(+-)1.0", "-1.0 .. 1.0"}, {"1.0(+-)0.005", "0.995 .. 1.005"},
	{"101(+-)1", "1.00e2 .. 1.02e2"}, {"100(+-)1", "99.0 .. 1.01e2"}, {"5(+-)0.3", "4.7 .. 5.3"}, {"10(+-)1", "9.0 .. 1.1e1"},
	{"1.0 .. 1.00", "1.0"}, {"0(+-)-1", "1 .. -1"}, {"~1(+-)1", "0 .. 2"},
}

func TestSegOutput(t *testing.T) {
	d := segType(t, 65000)
	for _, tc := range segCases {
		v := segInput(t, d, tc.in)
		out, e := d.Output(t.Context(), v)
		if e != nil || out != tc.out {
			t.Fatalf("%q output %q want %q (%v)", tc.in, out, tc.out, e)
		}
		literal, e := d.SQLLiteral(t.Context(), v)
		if e != nil || literal != "'"+tc.out+"'" {
			t.Fatal("SQL literal", literal, e)
		}
	}
	v := segInput(t, d, "1")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := d.Output(ctx, v); !errors.Is(e, context.Canceled) {
		t.Fatal("canceled output", e)
	}
}

func TestSegOutputNull(t *testing.T) {
	d := segType(t, 65000)
	literal, e := d.SQLLiteral(t.Context(), sqltype.Null(d))
	if e != nil || literal != "NULL" {
		t.Fatal("typed NULL literal", literal, e)
	}
}
