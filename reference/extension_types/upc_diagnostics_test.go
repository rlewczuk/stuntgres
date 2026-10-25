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

func TestReferenceUpcDiagnostics(t *testing.T) {
	for i, tc := range upcBadInputs() {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
				for _, bound := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("bound_%v_prepared_%v", bound, prepared), func(t *testing.T) {
							q := "SELECT " + ean13Literal(tc.input) + "::upc"
							var args []any
							want := ean13Diagnostic(tc.code, tc.message)
							want.Position = "8"
							if bound {
								q, args = "SELECT $1::upc", []any{tc.input}
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
				held := ean13Held(t, p, c, "SELECT $1::upc")
				for _, value := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(held.SQL, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$1"), value)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, upcSelect(b, held.SQL, []string{"upc"}, []string{"UPC"}, [][]any{{"123456789012!"}}, nil, "123456789010!"))
			})
		})
	}
	t.Run("character_positions", func(t *testing.T) {
		upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
			for _, q := range []string{"SELECT CAST('bad' AS upc)", "/*é*/ SELECT 'bad'::upc"} {
				for _, prepared := range []bool{false, true} {
					want := ean13Diagnostic("22P02", `invalid input syntax for UPC number: "bad"`)
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

func TestReferenceUpcAtomicity(t *testing.T) {
	upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE upc_atomic(id integer NOT NULL,v upc NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO upc_atomic VALUES(1,'123456789012')", 1))
		q := "INSERT INTO upc_atomic VALUES(2,$1),(3,$2)"
		held := ean13Held(t, p, c, q)
		seed := upcSelect(b, "SELECT id,v FROM upc_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "UPC"}, [][]any{{int64(1), "123456789012"}}, []bool{false, false})
		stamps := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13", "upc"} {
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
		for i, tc := range upcBadInputs() {
			t.Run(fmt.Sprintf("later_%02d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					literal := "INSERT INTO upc_atomic VALUES(2,'123456789012'),(3," + ean13Literal(tc.input) + ")"
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
					s := fixture.ErrorDiagnostic(q, isbnBoundDiagnostic(tc.code, tc.message, tc.input, "$2"), "123456789012", value)
					run(s)
					checkSeed()
					run(fixture.Prepared(s))
					checkSeed()
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					checkSeed()
				}
			})
		}
		want := ean13Diagnostic("23502", `null value in column "v" of relation "upc_atomic" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (3, null).", "public", "upc_atomic", "v"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO upc_atomic VALUES(2,'123456789012'),(3,NULL)", want)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			checkSeed()
			for _, value := range []any{nil, []byte(nil)} {
				s := fixture.ErrorDiagnostic(q, want, "123456789012", value)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				checkSeed()
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "123456789012", nil))
		checkSeed()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, "123456789012", []byte(nil)))
		checkSeed()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "123456789012", "123456789010!"))
		run(upcSelect(b, "SELECT id,v FROM upc_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "UPC"}, [][]any{{int64(1), "123456789012"}, {int64(2), "123456789012"}, {int64(3), "123456789012!"}}, []bool{false, false}))
		run(fixture.Exec("CREATE TABLE upc_atomic_nullable(id integer NOT NULL,v upc)", 0))
		nullable := ean13Held(t, p, c, "INSERT INTO upc_atomic_nullable VALUES(1,$1),(2,$2),(3,$3)")
		failure := isbnBoundDiagnostic("22P02", `invalid check digit for UPC number: "123456789010", should be 2`, "123456789010", "$3")
		fixture.RunStep(t, p.Ctx, nullable, b.Recorder, fixture.ErrorDiagnostic(nullable.SQL, failure, "123456789012", nil, []byte("123456789010")))
		run(upcSelect(b, "SELECT v FROM upc_atomic_nullable", []string{"v"}, []string{"UPC"}, [][]any{}, []bool{true}))
		fixture.RunStep(t, p.Ctx, nullable, b.Recorder, fixture.Exec(nullable.SQL, 3, "123456789012", []byte("123456789010!"), nil))
		run(upcSelect(b, "SELECT id,v FROM upc_atomic_nullable ORDER BY id", []string{"id", "v"}, []string{"INT4", "UPC"}, [][]any{{int64(1), "123456789012"}, {int64(2), "123456789012!"}, {int64(3), nil}}, []bool{false, true}))
		for typ, stamp := range stamps {
			if got := isbnIdentityStamp(t, p, c, typ); got != stamp { t.Fatalf("nullable insert changed %s identity: %v, want %v", typ, got, stamp) }
		}
	})
}