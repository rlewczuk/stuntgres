package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

type jsonbReferenceInvalid struct {
	name, input string
	want        referenceDiagnostic
}

func jsonbReferenceSyntax(name, input, detail, context string) jsonbReferenceInvalid {
	return jsonbReferenceInvalid{name, input, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: detail, Context: context}}
}
func jsonbReferenceInvalidCases() []jsonbReferenceInvalid {
	var cases []jsonbReferenceInvalid
	for _, tc := range jsonReferenceInvalidCases {
		cases = append(cases, jsonbReferenceInvalid{tc.name, tc.input, jsonReferenceDiagnostic(tc)})
	}
	for _, tc := range []struct{ name, input, detail, context string }{
		{"hex", `0x10`, `Token "0x10" is invalid.`, `JSON data, line 1: 0x10`},
		{"underscore", `1_000`, `Token "1_000" is invalid.`, `JSON data, line 1: 1_000`},
		{"missing_fraction", `1.`, `Token "1." is invalid.`, `JSON data, line 1: 1.`},
		{"missing_exponent_sign", `1e+`, `Token "1e+" is invalid.`, `JSON data, line 1: 1e+`},
		{"uppercase_true", `TRUE`, `Token "TRUE" is invalid.`, `JSON data, line 1: TRUE`},
		{"high_simple_escape", `"\uD800\n"`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: "\uD800\n...`},
		{"high_end", `"\uD800"`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: "\uD800"`},
		{"lone_low", `"\uDC00"`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: "\uDC00...`},
		{"high_high", `"\uD800\uD800"`, `Unicode high surrogate must not follow a high surrogate.`, `JSON data, line 1: "\uD800\uD800...`},
		{"high_ascii", `"\uD800a"`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: "\uD800a...`},
		{"high_nonlow_escape", `"\uD800\u0041"`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: "\uD800\u0041...`},
		{"reversed_pair", `"\uDC00\uD800"`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: "\uDC00...`},
		{"surrogate_key", `{"\uDC00":1}`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: {"\uDC00...`},
		{"discarded_surrogate", `{"a":"\uDC00","a":1}`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: {"a":"\uDC00...`},
		{"surrogate_before_syntax", `["\uDC00",]`, `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: ["\uDC00...`},
		{"malformed_overflow", `1e131072x`, `Token "1e131072x" is invalid.`, `JSON data, line 1: 1e131072x`},
		{"overflow_missing_fraction", `1e131072.`, `Token "." is invalid.`, `JSON data, line 1: 1e131072.`},
		{"syntax_before_unicode", `[no,"\u0000"]`, `Token "no" is invalid.`, `JSON data, line 1: [no...`},
		{"eof_unicode", `"\u`, `Token ""\u" is invalid.`, `JSON data, line 1: "\u`},
		{"multiline", "[\n \"\\uDC00\"]", `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 2:  "\uDC00...`},
	} {
		cases = append(cases, jsonbReferenceSyntax(tc.name, tc.input, tc.detail, tc.context))
	}
	for _, tc := range []struct{ name, input, context string }{
		{"unicode_zero", `"\u0000"`, `JSON data, line 1: "\u0000...`},
		{"zero_key", `{"\u0000":1}`, `JSON data, line 1: {"\u0000...`},
		{"discarded_zero", `{"a":"\u0000","a":1}`, `JSON data, line 1: {"a":"\u0000...`},
		{"zero_before_syntax", `["\u0000",]`, `JSON data, line 1: ["\u0000...`},
	} {
		cases = append(cases, jsonbReferenceInvalid{tc.name, tc.input, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P05", Message: "unsupported Unicode escape sequence", Detail: `\u0000 cannot be converted to text.`, Context: tc.context}})
	}
	for _, tc := range []struct{ name, input string }{
		{"magnitude_overflow", "1e131072"}, {"scale_overflow", "1e-16384"},
		{"mantissa_overflow", "10e131071"}, {"mantissa_scale_overflow", "0.1e-16383"},
		{"huge_exponent", "1e100000000000000000000"}, {"huge_zero_exponent", "0e100000000000000000000"},
		{"zero_scale_overflow", "0e-16384"}, {"trailing_scale_overflow", "0." + strings.Repeat("0", 16384)},
		{"discarded_overflow", `{"a":1e131072,"a":1}`},
		{"overflow_before_syntax", `[1e131072,]`}, {"overflow_before_unicode", `[1e131072,"\u0000"]`},
	} {
		cases = append(cases, jsonbReferenceInvalid{tc.name, tc.input, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "value overflows numeric format"}})
	}
	return cases
}

func TestReferenceJSONBDiagnostics(t *testing.T) {
	for _, tc := range jsonbReferenceInvalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE jb_errors(v JSONB)", 0))
				for _, prepared := range []bool{false, true} {
					for _, route := range []string{"postfix", "cast", "typed", "multibyte", "insert", "bound_select", "bound_insert"} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+route, func(t *testing.T) {
							quote := jsonReferenceQuote(tc.input)
							prefix := "SELECT "
							suffix := "::jsonb"
							selectRows := true
							bound := false
							switch route {
							case "cast":
								prefix = "SELECT CAST("
								suffix = " AS jsonb)"
							case "typed":
								prefix = "SELECT jsonb "
								suffix = ""
							case "multibyte":
								prefix = "/*é雪*/ SELECT "
							case "insert":
								prefix = "INSERT INTO jb_errors VALUES("
								suffix = ")"
								selectRows = false
							case "bound_select":
								bound = true
							case "bound_insert":
								prefix = "INSERT INTO jb_errors VALUES("
								suffix = ")"
								selectRows = false
								bound = true
							}
							want := tc.want
							var args []any
							q := prefix + quote + suffix
							want.Position = referencePosition(utf8.RuneCountInString(prefix) + 1)
							if bound {
								q = prefix + "$1" + suffix
								args = []any{tc.input}
								want.Position = ""
								if want.Context != "" {
									want.Context += "\n"
								}
								want.Context += "unnamed portal parameter $1 = '...'"
								if tc.input == "" {
									want.Context = tc.want.Context + "\nunnamed portal parameter $1 = ''"
								}
							}
							s := referenceErrorDiagnostic(q, want, args...)
							s.selectRows = selectRows
							s.errorStage = "execute"
							if prepared {
								s = referencePrepared(s)
								if !bound {
									s.errorStage = "prepare"
								}
							}
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						})
					}
				}
			}
		})
	}
}

func TestReferenceJSONBDiagnosticContext(t *testing.T) {
	cases := []jsonReferenceInvalid{
		{"bytes49", strings.Repeat("x", 49), `Token "` + strings.Repeat("x", 49) + `" is invalid.`, "JSON data, line 1: " + strings.Repeat("x", 49)},
		{"bytes50", strings.Repeat("x", 50), `Token "` + strings.Repeat("x", 50) + `" is invalid.`, "JSON data, line 1: " + strings.Repeat("x", 50)},
		{"bytes53", strings.Repeat("x", 53), `Token "` + strings.Repeat("x", 53) + `" is invalid.`, "JSON data, line 1: ..." + strings.Repeat("x", 49)},
		{"multibyte", strings.Repeat("é", 27), `Token "` + strings.Repeat("é", 27) + `" is invalid.`, "JSON data, line 1: ..." + strings.Repeat("é", 24)},
		{"cr_not_lf", "[\r1,]", `Expected JSON value, but found "]".`, "JSON data, line 1: [\r1,]"},
		{"eof_after_lf", "[1,\n  ", "The input string ended unexpectedly.", "JSON data, line 2:   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				for _, prepared := range []bool{false, true} {
					want := jsonReferenceDiagnostic(tc)
					want.Position = "8"
					s := referenceErrorDiagnostic("SELECT "+jsonReferenceQuote(tc.input)+"::jsonb", want)
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}

func TestReferenceJSONBBoundEncoding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arg     any
		message string
	}{
		{"invalid_utf8_string", string([]byte{255}), `invalid byte sequence for encoding "UTF8": 0xff`},
		{"invalid_utf8_bytes", []byte{255}, `invalid byte sequence for encoding "UTF8": 0xff`},
		{"nul_string", "\x00", `invalid byte sequence for encoding "UTF8": 0x00`},
		{"nul_bytes", []byte{0}, `invalid byte sequence for encoding "UTF8": 0x00`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				for _, prepared := range []bool{false, true} {
					want := controlsReferenceDiagnostic("22021", tc.message)
					want.Context = "unnamed portal parameter $1"
					s := referenceErrorDiagnostic("SELECT $1::jsonb", want, tc.arg)
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}

func jsonbReferenceAtomicity(t *testing.T, p *referencePair, backend referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
	run(referenceExec("CREATE TABLE jb_atomic(id INT,v JSONB)", 0))
	run(referenceExec("INSERT INTO jb_atomic VALUES(1,'null')", 1))
	q := "INSERT INTO jb_atomic VALUES(2,$1),(3,$2)"
	stmt, err := backend.db.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
	for _, name := range []string{"unicode_zero", "lone_low", "magnitude_overflow"} {
		for _, tc := range jsonbReferenceInvalidCases() {
			if tc.name != name {
				continue
			}
			t.Run(name, func(t *testing.T) {
				want := tc.want
				if want.Context != "" {
					want.Context += "\n"
				}
				want.Context += "unnamed portal parameter $2 = '...'"
				s := referenceErrorDiagnostic(q, want, `{"ok":1}`, tc.input)
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, held, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id,v FROM jb_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), "null"}}))
			})
		}
	}
	referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 2, ` {"ok":2} `, "null"))
	run(referenceSelect("SELECT id,v FROM jb_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), "null"}, {int64(2), `{"ok": 2}`}, {int64(3), "null"}}))
}
func TestReferenceJSONBInvalidBindAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		jsonbReferenceAtomicity(t, p, backend)
	}
}

func TestReferenceJSONBCancellationAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	var inputFields, outputFields []string
	for i := 255; i >= 0; i-- {
		inputFields = append(inputFields, fmt.Sprintf(`"k%04d":%d`, i, i))
	}
	inputFields = append(inputFields, `"\u006b0000":99`)
	for i := range 256 {
		value := i
		if i == 0 {
			value = 99
		}
		outputFields = append(outputFields, fmt.Sprintf(`"k%04d": %d`, i, value))
	}
	wide := "{" + strings.Join(inputFields, ",") + "}"
	canonicalWide := "{" + strings.Join(outputFields, ", ") + "}"
	longString := `"` + strings.Repeat("x", 8192) + `\n"`
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE jb_cancel(id INT,v JSONB)", 0))
			run(referenceExec(`INSERT INTO jb_cancel VALUES(1,'{"sentinel":true}')`, 1))
			q := "INSERT INTO jb_cancel VALUES(2,$1),(3,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			sentinel := referenceSelect("SELECT id,v FROM jb_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"sentinel": true}`}})
			// A pre-canceled public context is deterministic on database/sql and
			// pq: no race against a fast server query or fabricated PG57014.
			// Private checkpoint units cover cancellation during canonicalization.
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			for _, route := range []string{"direct", "held"} {
				t.Run(route, func(t *testing.T) {
					var result sql.Result
					var err error
					if route == "direct" {
						result, err = backend.db.ExecContext(canceled, q, wide, longString)
					} else {
						result, err = stmt.ExecContext(canceled, wide, longString)
					}
					if !errors.Is(err, context.Canceled) || result != nil {
						t.Fatal("canceled INSERT returned a result or wrong error", result, err)
					}
					run(sentinel)
				})
			}
			// The same held statement remains usable; independently assert wide
			// key order/decoded duplicate-last and long-string escaping afterward.
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 2, wide, longString))
			run(referenceSelect("SELECT id,v FROM jb_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"sentinel": true}`}, {int64(2), canonicalWide}, {int64(3), longString}}))
		})
	}
}
