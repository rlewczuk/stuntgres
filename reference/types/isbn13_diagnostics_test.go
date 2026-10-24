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

func TestReferenceIsbn13Diagnostics(t *testing.T) {
	for i, tc := range isbn13BadInputs() {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
				for _, bound := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("bound_%v_prepared_%v", bound, prepared), func(t *testing.T) {
							q := "SELECT " + ean13Literal(tc.input) + "::isbn13"
							var args []any
							want := ean13Diagnostic(tc.code, tc.message)
							want.Position = "8"
							if bound {
								q, args = "SELECT $1::isbn13", []any{tc.input}
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
				held := ean13Held(t, p, c, "SELECT $1::isbn13")
				for _, value := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(held.SQL, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$1"), value)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ean13Select(b, held.SQL, []string{"isbn13"}, []string{"ISBN13"}, [][]any{{"978-1-234-56789-7!"}}, nil, "1234567890!"))
			})
		})
	}
	t.Run("character_positions", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
			for _, q := range []string{"SELECT CAST('bad' AS isbn13)", "/*é*/ SELECT 'bad'::isbn13"} {
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

func TestReferenceIsbn13Atomicity(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE isbn13_atomic(id integer NOT NULL,v isbn13 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO isbn13_atomic VALUES(1,'9780123456786')", 1))
		q := "INSERT INTO isbn13_atomic VALUES(2,$1),(3,$2)"
		held := ean13Held(t, p, c, q)
		seed := ean13Select(b, "SELECT id,v FROM isbn13_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISBN13"}, [][]any{{int64(1), "978-0-12-345678-6"}}, []bool{false, false})
		for i, tc := range isbn13BadInputs() {
			t.Run(fmt.Sprintf("later_%02d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					literal := "INSERT INTO isbn13_atomic VALUES(2,'9780123456786'),(3," + ean13Literal(tc.input) + ")"
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
		want := ean13Diagnostic("23502", `null value in column "v" of relation "isbn13_atomic" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (3, null).", "public", "isbn13_atomic", "v"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO isbn13_atomic VALUES(2,'9780123456786'),(3,NULL)", want)
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
		run(ean13Select(b, "SELECT id,v FROM isbn13_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISBN13"}, [][]any{{int64(1), "978-0-12-345678-6"}, {int64(2), "978-0-12-345678-6"}, {int64(3), "978-1-234-56789-7!"}}, []bool{false, false}))
	})
}

func isbn13BadInputs() []struct{ input, code, message string } {
	out := isbnBadInputs()
	return append(out, struct{ input, code, message string }{"9791234567890", "22P02", `invalid check digit for ISBN number: "9791234567890", should be 6`})
}
