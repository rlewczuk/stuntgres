package table

import (
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAnalyzeOptions(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want AnalyzeOptions
	}{
		{"ANALYZE", AnalyzeOptions{}},
		{"ANALYSE VERBOSE t", AnalyzeOptions{Verbose: true}},
		{"ANALYZE (VERBOSE,SKIP_LOCKED) t", AnalyzeOptions{Verbose: true, SkipLocked: true}},
		{"ANALYZE (VERBOSE false,VERBOSE true,SKIP_LOCKED true,SKIP_LOCKED false)", AnalyzeOptions{Verbose: true}},
		{"ANALYZE (VERBOSE true,VERBOSE false)", AnalyzeOptions{}},
		{"ANALYZE (VERBOSE ON,SKIP_LOCKED OFF)", AnalyzeOptions{Verbose: true}},
		{"ANALYZE (VERBOSE 0,SKIP_LOCKED 1)", AnalyzeOptions{SkipLocked: true}},
		{"ANALYZE (VERBOSE 'true',SKIP_LOCKED 'off')", AnalyzeOptions{Verbose: true}},
		{"ANALYZE (BUFFER_USAGE_LIMIT 0)", AnalyzeOptions{Buffer: true}},
		{"ANALYZE (BUFFER_USAGE_LIMIT '128kB') ONLY t(a)", AnalyzeOptions{Buffer: true}},
		{"ANALYZE (BUFFER_USAGE_LIMIT '131072B')", AnalyzeOptions{Buffer: true}},
		{"ANALYZE (BUFFER_USAGE_LIMIT '16GB')", AnalyzeOptions{Buffer: true}},
		{"ANALYZE (BUFFER_USAGE_LIMIT 16777216)", AnalyzeOptions{Buffer: true}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			got, err := NormalizeAnalyze(admissionStatement(t, tc.q).(*parser.Analyze))
			if err != nil || got != tc.want {
				t.Fatalf("got %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestAnalyzeOptionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, position, hint string }{
		{"ANALYZE (WHAT true) t", "42601", `unrecognized ANALYZE option "what"`, "10", ""},
		{"/*é*/ ANALYSE (WHAT) t", "42601", `unrecognized ANALYZE option "what"`, "16", ""},
		{"ANALYZE (FULL)", "42601", `unrecognized ANALYZE option "full"`, "10", ""},
		{"ANALYZE (VERBOSE 'nonsense')", "42601", "verbose requires a Boolean value", "", ""},
		{"ANALYZE (SKIP_LOCKED 'bad')", "42601", "skip_locked requires a Boolean value", "", ""},
		{"ANALYZE (VERBOSE 2)", "42601", "verbose requires a Boolean value", "", ""},
		{"ANALYZE (VERBOSE 'bad',VERBOSE true)", "42601", "verbose requires a Boolean value", "", ""},
		{"ANALYZE (BUFFER_USAGE_LIMIT)", "42601", "buffer_usage_limit requires a parameter", "", ""},
		{"ANALYZE (BUFFER_USAGE_LIMIT '-1')", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", ""},
		{"ANALYZE (BUFFER_USAGE_LIMIT 127)", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", ""},
		{"ANALYZE (BUFFER_USAGE_LIMIT 16777217)", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", ""},
		{"ANALYZE (BUFFER_USAGE_LIMIT '128 zz')", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`},
		{"ANALYZE (BUFFER_USAGE_LIMIT '999999999999999999999kB')", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", "Value exceeds integer range."},
		{"ANALYZE (WHAT,VERBOSE 'bad')", "42601", `unrecognized ANALYZE option "what"`, "10", ""},
		{"ANALYZE (VERBOSE 'bad',WHAT)", "42601", "verbose requires a Boolean value", "", ""},
	} {
		t.Run(tc.q, func(t *testing.T) {
			_, err := NormalizeAnalyze(admissionStatement(t, tc.q).(*parser.Analyze))
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
			if !errors.As(err, &got) || *got != *want {
				t.Fatalf("got %#v want %#v", got, want)
			}
		})
	}
}

func TestAnalyzePreservesVacuumAnalyzeBoundary(t *testing.T) {
	o, err := NormalizeVacuum(admissionStatement(t, "VACUUM ANALYZE t(a)").(*parser.Vacuum))
	if err != nil {
		t.Fatal(err)
	}
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "VACUUM ANALYZE is not supported"}
	if err := CheckVacuumEntry(o, false); !errors.As(err, &got) || *got != *want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
