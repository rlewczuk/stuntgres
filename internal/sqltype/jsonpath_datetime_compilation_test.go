package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONPathDatetimeCompilerModes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ source, template, invalid string }{
		{"2023X08X15", `YYYY"X"MM"X"DD`, ""},
		{"2023X08X15", `YYYYXMMXDD`, "X"},
		{"2023年08月15日", `YYYY"年"MM"月"DD"日"`, ""},
		{"2023年08月15日", `YYYY年MM月DD日`, "年"},
		{"2023-08-15X", `YYYY-MM-DD"X"`, ""},
		{"2023-08-15X", `YYYY-MM-DDX`, "X"},
		{"2023-08-15年", `YYYY-MM-DD"年"`, ""},
		{"2023-08-15年", `YYYY-MM-DD年`, "年"},
		{"2023_08_15", `YYYY_MM_DD`, "_"},
	} {
		t.Run(tc.template, func(t *testing.T) {
			nodes, err := compileDatetime(ctx, tc.template, datetimeJSONPathFormat)
			if tc.invalid != "" {
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid datetime format separator: "` + tc.invalid + `"`}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("compiler diagnostic=%+v want=%+v", diagnostic, want)
				}
				marked, ok := err.(*sqlstate.InputError)
				if !ok || marked.Recoverable() {
					t.Fatal("compiler error lost hard producer provenance", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				value, err := datetimeInput(ctx, nil, tc.source, nodes, InputEnvironment{}, false, true, nil)
				if err != nil || value.typ.id != Date || value.integer != 8627 {
					t.Fatal(value, err)
				}
			}
			// Ordinary SQL masks use the same compiler without strict lexical
			// admission. Every corresponding date still uses the shared reader.
			nodes, err = compileDatetime(ctx, tc.template, datetimeSQLFormat)
			if err != nil {
				t.Fatal(err)
			}
			value, err := datetimeInput(ctx, jsonpathDatetimeType(Date), tc.source, nodes, InputEnvironment{}, false, false, nil)
			if err != nil || value.integer != 8627 {
				t.Fatal("ordinary formatting changed", value, err)
			}
		})
	}
	// Polling must remain effective even when almost all of the mask consists
	// of admitted quoted text and the invalid rune is late in the template.
	ctxCancel := &groupingCancelContext{Context: ctx, at: 4}
	_, err := compileDatetime(ctxCancel, strings.Repeat(`"x"`, 4096)+"YYYYX", datetimeJSONPathFormat)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("strict compiler did not preserve cancellation", err)
	}
}

func TestJSONPathDatetimeCompilerErrorChannel(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		doc, path string
		hard      bool
		count     int
	}{
		{`"2023_08_15"`, `$.datetime("YYYY_MM_DD")`, true, 0},
		{`"bad"`, `$.datetime("YYYY_MM_DD")`, true, 0},
		{`"2023-08-15"`, `$.datetime("YYYY-MM-DDX")`, true, 0},
		{`"2023_08_15"`, `exists($.datetime("YYYY_MM_DD"))`, true, 0},
		{`"2023/08/15"`, `$.datetime("YYYY-MM-DD")`, false, 0},
		{`"2023/08/15"`, `exists($.datetime("YYYY-MM-DD"))`, false, 1},
	} {
		values := navigateValues(t, types, tc.doc, tc.path, `{}`, true)
		count := 0
		status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(jsonpathItem) (bool, error) { count++; return true, nil })
		if tc.hard {
			marked, ok := err.(*sqlstate.InputError)
			if !ok || marked.Recoverable() || status.kind == jsonpathSuppressibleStop {
				t.Fatal("compiler error became suppressible", status, err)
			}
		} else if tc.count == 0 {
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `unmatched format separator "-"`}
			var diagnostic *sqlstate.Error
			if err != nil || status.kind != jsonpathSuppressibleStop || !errors.As(status.diagnostic, &diagnostic) || *diagnostic != want {
				t.Fatal("reader error lost recoverable channel", status, err)
			}
		} else if err != nil || status.kind != jsonpathComplete {
			t.Fatal("recoverable predicate did not become unknown", status, err)
		}
		if count != tc.count {
			t.Fatal("unexpected emitted count", count, tc.count)
		}
	}
}

func TestJSONPathDatetimeRawMessage(t *testing.T) {
	for _, method := range []string{"datetime", "date", "time", "time_tz", "timestamp", "timestamp_tz"} {
		for _, input := range []string{`bad"date`, `bad\date`, "bad\tdate", "bad\ndate", "bad\rdate", "bad\x01date"} {
			err := jsonpathDatetimeUnrecognized(method, input)
			structural, ok := err.(*jsonpathStructuralError)
			if !ok {
				t.Fatal("ISO input failure lost structural provenance", err)
			}
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22031", Message: method + ` format is not recognized: "` + input + `"`}
			if method == "datetime" {
				want.Hint = "Use a datetime template argument to specify the input data format."
			}
			if !errors.As(structural.diagnostic, &diagnostic) || *diagnostic != want {
				t.Fatalf("raw diagnostic=%+v want=%+v", diagnostic, want)
			}
		}
	}
}

func TestJSONPathDatetimeSQLJSONErrorPhases(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		name, kind, document, path, code, message string
		result                                    ID
		ordinary, hardInput                       bool
	}{
		{"query-compiler", "json_query", `"2023_08_15"`, `$.datetime("YYYY_MM_DD")`, "22007", `invalid datetime format separator: "_"`, JSONB, true, true},
		{"value-compiler", "json_value", `"bad"`, `$.datetime("YYYY_MM_DD")`, "22007", `invalid datetime format separator: "_"`, Text, true, true},
		{"query-reader", "json_query", `"2023/08/15"`, `$.datetime("YYYY-MM-DD")`, "22007", `unmatched format separator "-"`, JSONB, false, false},
		{"value-reader", "json_value", `"2023/08/15"`, `$.datetime("YYYY-MM-DD")`, "22007", `unmatched format separator "-"`, Text, false, false},
		// Destination conversion has its existing SQLJSON diagnostic-class
		// policy, independent of the source's COPY/input-soft annotation.
		{"destination-hard-input", "json_value", `"1e131072"`, `$`, "22003", "value overflows numeric format", JSONPath, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := BindSQLJSONQuery(tc.kind, "", false, types[tc.result].Usage(), types[JSONB].Usage(), types[JSONPath].Usage(), types[JSONB].Usage(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			document := jsonbSetUnitInput(t, types[JSONB], tc.document)
			path := jsonbSetUnitInput(t, types[JSONPath], tc.path)
			outcome, err := plan.Evaluate(ctx, document, path, nil, JSONPathEnvironment{}, OutputEnvironment{})
			cause := err
			if tc.ordinary {
				if err == nil || outcome.Error != nil {
					t.Fatal("compiler failure entered ON ERROR", outcome, err)
				}
			} else {
				if err != nil || outcome.Error == nil {
					t.Fatal("recoverable phase bypassed ON ERROR", outcome, err)
				}
				cause = outcome.Error
			}
			if outcome.Empty || outcome.Value != (Value{}) {
				t.Fatal("failed phase published a value", outcome)
			}
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(cause, &diagnostic) || *diagnostic != want {
				t.Fatalf("diagnostic=%+v want=%+v", diagnostic, want)
			}
			if tc.hardInput {
				marked, ok := cause.(*sqlstate.InputError)
				if !ok || marked.Recoverable() || InputSoftError(cause) {
					t.Fatal("source input annotation changed", cause)
				}
			}
		})
	}
}
