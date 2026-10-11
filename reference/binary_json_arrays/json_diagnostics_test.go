package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Complete records use independently specified, pinned-PG18-validated expectations.
type jsonReferenceInvalid struct{ name, input, detail, context string }

var jsonReferenceInvalidCases = []jsonReferenceInvalid{
	{"empty", "", "The input string ended unexpectedly.", "JSON data, line 1: "},
	{"whitespace", " \t\r\n ", "The input string ended unexpectedly.", "JSON data, line 2:  "},
	{"keyword", "xyz", `Token "xyz" is invalid.`, "JSON data, line 1: xyz"},
	{"incomplete_keyword", "tru", `Token "tru" is invalid.`, "JSON data, line 1: tru"},
	{"leading_zero", "01", `Token "01" is invalid.`, "JSON data, line 1: 01"},
	{"plus", "+1", `Token "+" is invalid.`, "JSON data, line 1: +..."},
	{"nan", "NaN", `Token "NaN" is invalid.`, "JSON data, line 1: NaN"},
	{"infinity", "Infinity", `Token "Infinity" is invalid.`, "JSON data, line 1: Infinity"},
	{"number_junk", "1f2", `Token "1f2" is invalid.`, "JSON data, line 1: 1f2"},
	{"fraction_junk", "0.x1", `Token "0.x1" is invalid.`, "JSON data, line 1: 0.x1"},
	{"exponent_junk", "1.3ex100", `Token "1.3ex100" is invalid.`, "JSON data, line 1: 1.3ex100"},
	{"incomplete_exponent", "1e", `Token "1e" is invalid.`, "JSON data, line 1: 1e"},
	{"extra_value", "true false", `Expected end of input, but found "false".`, "JSON data, line 1: true false"},
	{"closing_token", "]", `Expected JSON value, but found "]".`, "JSON data, line 1: ]"},
	{"array_trailing_comma", "[1,2,]", `Expected JSON value, but found "]".`, "JSON data, line 1: [1,2,]"},
	{"array_first_comma", "[,]", `Expected JSON value, but found ",".`, "JSON data, line 1: [,..."},
	{"array_missing_comma", "[1 2]", `Expected "," or "]", but found "2".`, "JSON data, line 1: [1 2..."},
	{"object_missing_colon", `{"a" 1}`, `Expected ":", but found "1".`, `JSON data, line 1: {"a" 1...`},
	{"object_numeric_key", `{1:"abc"}`, `Expected string or "}", but found "1".`, "JSON data, line 1: {1..."},
	{"object_trailing_comma", `{"a":1,}`, `Expected string, but found "}".`, `JSON data, line 1: {"a":1,}`},
	{"object_missing_value", `{"a":}`, `Expected JSON value, but found "}".`, `JSON data, line 1: {"a":}`},
	{"object_missing_comma", `{"a":1 "b":2}`, `Expected "," or "}", but found ""b"".`, `JSON data, line 1: {"a":1 "b"...`},
	{"unfinished_array", "[1,", "The input string ended unexpectedly.", "JSON data, line 1: [1,"},
	{"unfinished_object", `{"a":`, "The input string ended unexpectedly.", `JSON data, line 1: {"a":`},
	{"unfinished_string", `"abc`, `Token ""abc" is invalid.`, `JSON data, line 1: "abc`},
	{"illegal_escape", `"\v"`, `Escape sequence "\v" is invalid.`, `JSON data, line 1: "\v...`},
	{"unicode_empty", `"\u"`, `"\u" must be followed by four hexadecimal digits.`, `JSON data, line 1: "\u"`},
	{"unicode_short", `"\u00"`, `"\u" must be followed by four hexadecimal digits.`, `JSON data, line 1: "\u00"`},
	{"unicode_nonhex", `"\u000g"`, `"\u" must be followed by four hexadecimal digits.`, `JSON data, line 1: "\u000g...`},
	{"raw_tab", "\"a\tb\"", "Character with value 0x09 must be escaped.", `JSON data, line 1: "a...`},
	{"raw_lf", "\"a\nb\"", "Character with value 0x0a must be escaped.", `JSON data, line 1: "a`},
	{"raw_cr", "\"a\rb\"", "Character with value 0x0d must be escaped.", `JSON data, line 1: "a`},
	{"raw_control", "\"a\x01b\"", "Character with value 0x01 must be escaped.", `JSON data, line 1: "a...`},
	{"multiline", "[\n 1,\n ]", `Expected JSON value, but found "]".`, "JSON data, line 3:  ]"},
}

func jsonReferenceDiagnostic(tc jsonReferenceInvalid) fixture.Diagnostic {
	return fixture.Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
		Message: "invalid input syntax for type json", Detail: tc.detail, Context: tc.context,
	}
}

func TestReferenceJSONInvalidSyntax(t *testing.T) {
	for _, tc := range jsonReferenceInvalidCases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
						t.Run(prefix+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							want := jsonReferenceDiagnostic(tc)
							want.Position = "8"
							if prefix != "SELECT " {
								want.Position = "14"
							}
							s := fixture.ErrorDiagnostic(prefix+jsonReferenceQuote(tc.input)+"::json", want)
							s.SelectRows = true
							s.ErrorStage = "execute"
							if prepared {
								s = fixture.Prepared(s)
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						})
					}
					t.Run(map[bool]string{false: "bound_direct", true: "bound_prepared"}[prepared], func(t *testing.T) {
						want := jsonReferenceDiagnostic(tc)
						// PG18 redacts nonempty text, including control bytes; empty
						// input retains an explicitly empty parameter value.
						want.Context += "\nunnamed portal parameter $1 = '...'"
						if tc.input == "" {
							want.Context = tc.context + "\nunnamed portal parameter $1 = ''"
						}
						s := fixture.ErrorDiagnostic("SELECT $1::json", want, tc.input)
						s.SelectRows = true
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					})
				}
			}
		})
	}
}

func TestReferenceJSONInvalidBindAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE json_atomic(id INTEGER,v JSON)", 0))
		run(fixture.Exec("INSERT INTO json_atomic VALUES(1,'null')", 1))
		q := "INSERT INTO json_atomic VALUES(2,$1),(3,$2)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: `Expected JSON value, but found "]".`, Context: "JSON data, line 1: [1,2,]\nunnamed portal parameter $2 = '...'"}
		s := fixture.ErrorDiagnostic(q, want, ` {"ok":1} `, "[1,2,]")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		run(fixture.Select("SELECT id,v FROM json_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), "null"}}))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 2, ` {"ok":2} `, "null"))
		run(fixture.Select("SELECT id,v FROM json_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), "null"}, {int64(2), ` {"ok":2} `}, {int64(3), "null"}}))
	}
}
