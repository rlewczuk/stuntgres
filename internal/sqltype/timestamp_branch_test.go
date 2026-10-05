package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTimestampSignedWhitespaceAllocations(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	for _, sign := range []string{"+", "-"} {
		for _, whitespace := range []string{" ", "\t", "\n", "\r", "\f", "\v", strings.Repeat(" ", 200)} {
			for _, special := range []bool{false, true} {
				input := "1999-01-08 04:05:06 " + sign + whitespace + "8"
				want := "1999-01-08 04:05:06"
				if special {
					input = sign + whitespace + "InFiNiTy"
					want = "infinity"
					if sign == "-" {
						want = "-infinity"
					}
				}
				t.Run(input, func(t *testing.T) {
					var value Value
					var err error
					allocations := testing.AllocsPerRun(1000, func() { value, err = d.Input(ctx, input, -1) })
					if err != nil {
						t.Fatal(err)
					}
					out, err := d.Output(ctx, value)
					if err != nil || out != want {
						t.Fatal(out, want, err)
					}
					if allocations != 0 {
						t.Fatalf("borrowed signed input allocated %g times/run, want 0", allocations)
					}
				})
			}
		}
	}
}

func TestTimestampBranchInputAllocations(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	for _, tc := range []struct{ input, want string }{
		{"1999 1 8.5", "1999-01-08 00:00:00.5"},
		{"1999-01-08 250000.5", "1999-01-09 01:00:00.5"},
		{"1999-01-08 049900", "1999-01-08 05:39:00"},
		{"J on UTC 2451187", "1999-01-08 00:00:00"},
		{"1999-01-08 T on 040506", "1999-01-08 04:05:06"},
		{"1999-01-08 04:05:06 PsT DsT", "1999-01-08 04:05:06"},
		{"on - InFiNiTy at", "-infinity"},
		{"January 8 04:05:06 120000 PST", "120000-01-08 04:05:06"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var value Value
			var err error
			allocations := testing.AllocsPerRun(1000, func() { value, err = d.Input(ctx, tc.input, -1) })
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Output(ctx, value)
			if err != nil || out != tc.want {
				t.Fatal(out, tc.want, err)
			}
			if allocations != 0 {
				t.Fatalf("successful branch input allocated %g times/run, want 0", allocations)
			}
		})
	}
}

func TestTimestampDSTClassificationAndDateCompletion(t *testing.T) {
	d := testTypes()[Timestamp]
	ctx := context.Background()
	for _, tc := range []struct {
		zone     string
		admitted bool
	}{
		{"PST", true}, {"UTC", true}, {"+02", true}, {"EDT", false}, {"MSK", false}, {"America/New_York", false}, {"FOOBAR0", false},
	} {
		for _, suffix := range []string{tc.zone + " dst", "dst " + tc.zone} {
			input := "1999-01-08 04:05:06 " + suffix
			value, err := d.Input(ctx, input, -1)
			if tc.admitted {
				if err != nil {
					t.Fatal(input, err)
				}
				out, err := d.Output(ctx, value)
				if err != nil || out != "1999-01-08 04:05:06" {
					t.Fatal(input, out, err)
				}
			} else {
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid input syntax for type timestamp: "` + input + `"`}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("input %q got %#v (%v), want %#v", input, got, err, want)
				}
			}
		}
	}
}
