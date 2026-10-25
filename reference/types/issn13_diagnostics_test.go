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

func TestReferenceIssn13Diagnostics(t *testing.T) {
	for i, tc := range issnBadInputs() {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
				for _, bound := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("bound_%v_prepared_%v", bound, prepared), func(t *testing.T) {
							q := "SELECT " + ean13Literal(tc.input) + "::issn13"
							var args []any
							want := ean13Diagnostic(tc.code, tc.message)
							want.Position = "8"
							if bound {
								q, args = "SELECT $1::issn13", []any{tc.input}
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
				held := ean13Held(t, p, c, "SELECT $1::issn13")
				for _, value := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(held.SQL, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$1"), value)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ean13Select(b, held.SQL, []string{"issn13"}, []string{"ISSN13"}, [][]any{{"977-1234-567-00-3!"}}, nil, "12345670!"))
			})
		})
	}
	t.Run("character_positions", func(t *testing.T) {
		issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
			for _, q := range []string{"SELECT CAST('bad' AS issn13)", "/*é*/ SELECT 'bad'::issn13"} {
				for _, prepared := range []bool{false, true} {
					want := ean13Diagnostic("22P02", `invalid input syntax for ISSN number: "bad"`)
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

func TestReferenceIssn13Atomicity(t *testing.T) {
	issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE issn13_atomic(id integer NOT NULL,v issn13 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO issn13_atomic VALUES(1,'12345679')", 1))
		q := "INSERT INTO issn13_atomic VALUES(2,$1),(3,$2)"
		held := ean13Held(t, p, c, q)
		seed := ean13Select(b, "SELECT id,v FROM issn13_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISSN13"}, [][]any{{int64(1), "977-1234-567-00-3"}}, []bool{false, false})
		stamps := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
			stamps[typ] = isbnIdentityStamp(t, p, c, typ)
		}
		var extension int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='isn'").Scan(&extension); err != nil {
			t.Fatal(err)
		}
		checkSeed := func() {
			run(seed)
			for typ, stamp := range stamps {
				if got := isbnIdentityStamp(t, p, c, typ); got != stamp {
					t.Fatalf("rejected insert changed %s identities: %v, want %v", typ, got, stamp)
				}
			}
			var got int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='isn'").Scan(&got); err != nil || got != extension {
				t.Fatalf("rejected insert changed extension identity: %d, %v", got, err)
			}
		}
		for i, tc := range issnBadInputs() {
			t.Run(fmt.Sprintf("later_%02d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					literal := "INSERT INTO issn13_atomic VALUES(2,'12345679'),(3," + ean13Literal(tc.input) + ")"
					want := ean13Diagnostic(tc.code, tc.message)
					want.Position = strconv.Itoa(utf8.RuneCountInString(literal[:strings.LastIndex(literal, ean13Literal(tc.input))]) + 1)
					s := fixture.ErrorDiagnostic(literal, want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					checkSeed()
				}
				for _, value := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(q, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$2"), "12345679", value)
					run(s)
					checkSeed()
					run(fixture.Prepared(s))
					checkSeed()
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					checkSeed()
				}
			})
		}
		want := ean13Diagnostic("23502", `null value in column "v" of relation "issn13_atomic" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (3, null).", "public", "issn13_atomic", "v"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO issn13_atomic VALUES(2,'12345679'),(3,NULL)", want)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			checkSeed()
			for _, value := range []any{nil, []byte(nil)} {
				s := fixture.ErrorDiagnostic(q, want, "12345679", value)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				checkSeed()
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "12345679", nil))
		checkSeed()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "12345679", []byte(nil)))
		checkSeed()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "12345679", "12345670!"))
		run(ean13Select(b, "SELECT id,v FROM issn13_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISSN13"}, [][]any{{int64(1), "977-1234-567-00-3"}, {int64(2), "977-1234-567-00-3"}, {int64(3), "977-1234-567-00-3!"}}, []bool{false, false}))
	})
}
