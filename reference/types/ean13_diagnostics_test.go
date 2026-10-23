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

var ean13Failures = []struct{ input, code, message string }{
	{"1234567890120", "22P02", `invalid check digit for EAN13 number: "1234567890120", should be 8`},
	{"postgresql...", "22P02", `invalid input syntax for EAN13 number: "postgresql..."`},
	{"", "22P02", `invalid input syntax for EAN13 number: ""`},
	{"123456789012", "22P02", `cannot cast UPC to EAN13 for number: "123456789012"`},
	{"123456789X", "22P02", `cannot cast ISBN to EAN13 for number: "123456789X"`},
	{"M123456785", "22P02", `cannot cast ISMN to EAN13 for number: "M123456785"`},
	{"12345679", "22P02", `cannot cast ISSN to EAN13 for number: "12345679"`},
	{"12345678901234", "22003", `value "12345678901234" is out of range for EAN13 type`},
}

// These malformed precedence expectations are source-derived, not native-derived.
// Fixture failure diagnostics expose all fields for oracle contradictions.
func ean13BadInputs() []struct{ input, code, message string } {
	out := append([]struct{ input, code, message string }(nil), ean13Failures...)
	for _, input := range []string{"1", "12345678901", "123456A890128", "123\t4567890128", "123\n4567890128", "!1234567890128", "1234567890128!!", "12345678901?8", "1234567890128junk"} {
		out = append(out, struct{ input, code, message string }{input, "22P02", "invalid input syntax for EAN13 number: \"" + input + "\""})
	}
	return out
}

func TestReferenceEan13Diagnostics(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
		for i, tc := range ean13BadInputs() {
			t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
				// Keep each route independent so an oracle contradiction does not
				// hide diagnostics for the other origins or malformed inputs.
				for _, bound := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("bound_%v_prepared_%v", bound, prepared), func(t *testing.T) {
							q := "SELECT " + ean13Literal(tc.input) + "::ean13"
							args := []any(nil)
							want := ean13Diagnostic(tc.code, tc.message)
							want.Position = "8"
							if bound {
								q, args, want.Position = "SELECT $1::ean13", []any{tc.input}, ""
								text := "..."
								if tc.input == "" {
									text = ""
								}
								want.Context = "unnamed portal parameter $1 = '" + text + "'"
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
			})
		}
		for _, q := range []string{"SELECT CAST('bad' AS ean13)", "/*é*/ SELECT 'bad'::ean13"} {
			for _, prepared := range []bool{false, true} {
				want := ean13Diagnostic("22P02", `invalid input syntax for EAN13 number: "bad"`)
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
}

func TestReferenceEan13Atomicity(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE ean_atomic(id integer NOT NULL,v ean13 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO ean_atomic VALUES(1,'0123456789012')", 1))
		q := "INSERT INTO ean_atomic VALUES(2,$1),(3,$2)"
		held := ean13Held(t, p, c, q)
		seed := ean13Select(b, "SELECT id,v FROM ean_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "EAN13"}, [][]any{{int64(1), "012-345678901-2"}}, []bool{false, false})
		for i, tc := range ean13BadInputs() {
			t.Run(fmt.Sprintf("later_%02d", i), func(t *testing.T) {
				t.Run("literal", func(t *testing.T) {
					q := "INSERT INTO ean_atomic VALUES(2,'9780123456786'),(3," + ean13Literal(tc.input) + ")"
					want := ean13Diagnostic(tc.code, tc.message)
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, ean13Literal(tc.input))]) + 1)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, want))
				})
				t.Run("held", func(t *testing.T) {
					want := ean13Diagnostic(tc.code, tc.message)
					text := "..."
					if tc.input == "" {
						text = ""
					}
					want.Context = "unnamed portal parameter $2 = '" + text + "'"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "9780123456786", tc.input))
				})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, seed)
			})
		}
		want := ean13Diagnostic("23502", `null value in column "v" of relation "ean_atomic" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (3, null).", "public", "ean_atomic", "v"
		run(fixture.ErrorDiagnostic("INSERT INTO ean_atomic VALUES(2,'9780123456786'),(3,NULL)", want))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "9780123456786", nil))
		run(seed)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "9780123456786", "1234567890120!"))
		run(ean13Select(b, "SELECT id,v FROM ean_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "EAN13"}, [][]any{{int64(1), "012-345678901-2"}, {int64(2), "978-0-12-345678-6"}, {int64(3), "123-456789012-8!"}}, []bool{false, false}))
	})
}
