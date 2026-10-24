package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

var isbnFailures = []struct{ input, code, message string }{
	{"1234567890", "22P02", `invalid check digit for ISBN number: "1234567890", should be X`},
	{"9780123456780", "22P02", `invalid check digit for ISBN number: "9780123456780", should be 6`},
	{"9790123456785", "22P02", `cannot cast ISMN to ISBN for number: "9790123456785"`},
	{"9771234567898", "22P02", `cannot cast ISSN to ISBN for number: "9771234567898"`},
	{"0123456789012", "22P02", `cannot cast UPC to ISBN for number: "0123456789012"`},
	{"1234567890128", "22P02", `cannot cast EAN13 to ISBN for number: "1234567890128"`},
	{"12345678901234", "22003", `value "12345678901234" is out of range for ISBN type`},
	{"9790123456780!", "22P02", `cannot cast ISMN to ISBN for number: "9790123456780!"`},
	{"9771234567890!", "22P02", `cannot cast ISSN to ISBN for number: "9771234567890!"`},
	{"1234567890120!", "22P02", `cannot cast EAN13 to ISBN for number: "1234567890120!"`},
}

func isbnBadInputs() []struct{ input, code, message string } {
	out := append([]struct{ input, code, message string }(nil), isbnFailures...)
	for _, input := range []string{"postgresql...", "", "1", "12345678901", "123456A89X", "123\t456789X", "123\n456789X", "!123456789X", "123456789X!!", "12345678?X", "123456789Xjunk", "123456789X ", "123456789X-", "123456A89X!"} {
		out = append(out, struct{ input, code, message string }{input, "22P02", "invalid input syntax for ISBN number: \"" + input + "\""})
	}
	return out
}

// lib/pq's default error parameter limit redacts every nonempty bind value.
// Positions and portal contexts remain independent complete diagnostic records.
func isbnBoundDiagnostic(code, message, input, parameter string) fixture.Diagnostic {
	want := ean13Diagnostic(code, message)
	text := "..."
	if input == "" {
		text = ""
	}
	want.Context = "unnamed portal parameter " + parameter + " = '" + text + "'"
	return want
}

func TestReferenceIsbnDiagnostics(t *testing.T) {
	for i, tc := range isbnBadInputs() {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
				for _, bound := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("bound_%v_prepared_%v", bound, prepared), func(t *testing.T) {
							q := "SELECT " + ean13Literal(tc.input) + "::isbn"
							var args []any
							want := ean13Diagnostic(tc.code, tc.message)
							want.Position = "8"
							if bound {
								q, args = "SELECT $1::isbn", []any{tc.input}
								want = isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$1")
							}
							s := fixture.ErrorDiagnostic(q, want, args...)
							s.SelectRows, s.ErrorStage = true, "execute"
							if prepared {
								s = fixture.Prepared(s)
								if !bound {
									s.ErrorStage = "prepare"
								}
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						})
					}
				}
				held := ean13Held(t, p, c, "SELECT $1::isbn")
				for _, value := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(held.SQL, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$1"), value)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ean13Select(b, held.SQL, []string{"isbn"}, []string{"ISBN"}, [][]any{{"1-234-56789-X!"}}, nil, "1234567890!"))
			})
		})
	}
	t.Run("character_positions", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
			for _, q := range []string{"SELECT CAST('bad' AS isbn)", "/*é*/ SELECT 'bad'::isbn"} {
				for _, prepared := range []bool{false, true} {
					want := ean13Diagnostic("22P02", `invalid input syntax for ISBN number: "bad"`)
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)
					s := fixture.ErrorDiagnostic(q, want)
					s.SelectRows, s.ErrorStage = true, "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		})
	})
}

func TestReferenceIsbnAtomicity(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE isbn_atomic(id integer NOT NULL,v isbn NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO isbn_atomic VALUES(1,'9780123456786')", 1))
		q := "INSERT INTO isbn_atomic VALUES(2,$1),(3,$2)"
		held := ean13Held(t, p, c, q)
		seed := ean13Select(b, "SELECT id,v FROM isbn_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISBN"}, [][]any{{int64(1), "0-12-345678-9"}}, []bool{false, false})
		for i, tc := range isbnBadInputs() {
			t.Run(fmt.Sprintf("later_%02d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					literal := "INSERT INTO isbn_atomic VALUES(2,'9780123456786'),(3," + ean13Literal(tc.input) + ")"
					want := ean13Diagnostic(tc.code, tc.message)
					want.Position = strconv.Itoa(utf8.RuneCountInString(literal[:strings.LastIndex(literal, ean13Literal(tc.input))]) + 1)
					s := fixture.ErrorDiagnostic(literal, want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					run(seed)
				}
				for _, value := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(q, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$2"), "9780123456786", value)
					run(s)
					run(seed)
					run(fixture.Prepared(s))
					run(seed)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					run(seed)
				}
			})
		}
		want := ean13Diagnostic("23502", `null value in column "v" of relation "isbn_atomic" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (3, null).", "public", "isbn_atomic", "v"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO isbn_atomic VALUES(2,'9780123456786'),(3,NULL)", want)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			run(seed)
			for _, value := range []any{nil, []byte(nil)} {
				s := fixture.ErrorDiagnostic(q, want, "9780123456786", value)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(seed)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "9780123456786", nil))
		run(seed)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "9780123456786", "1234567890!"))
		run(ean13Select(b, "SELECT id,v FROM isbn_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISBN"}, [][]any{{int64(1), "0-12-345678-9"}, {int64(2), "0-12-345678-9"}, {int64(3), "1-234-56789-X!"}}, []bool{false, false}))
	})
}
