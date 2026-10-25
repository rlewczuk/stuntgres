package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Published REL_18_6 examples and independently derived checksum/range boundaries.
var issnValues = []struct{ input, output string }{
	{"12345679", "1234-5679"},
	{"1234-5679", "1234-5679"},
	{"9771234567003", "1234-5679"},
	{"1436-4522", "1436-4522"},
	{"1144875x", "1144-875X"},
	{"1144875X", "1144-875X"},
	{"1144875?", "1144-875X"},
	{"3251231?", "3251-2317"},
	{"00000000", "0000-0000"},
	{"99999994", "9999-9994"},
	{"12345679 ", "1234-5679"},
	{"12345679-", "1234-5679"},
	{"12345670!", "1234-5679!"},
	{"12345679!", "1234-5679!"},
	{"9771234567000!", "1234-5679!"},
	{"1234567?!", "1234-5679"},
	{"9771234567898", "977-1234-567-89-8"},
	{"9771234567898!", "977-1234-567-89-8!"},
}

func TestReferenceIssnValues(t *testing.T) {
	for i, tc := range issnValues {
		t.Run(fmt.Sprintf("value_%02d", i), func(t *testing.T) {
			issnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				output := issnOutput(b, tc.output)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE issn_values(id integer NOT NULL,v issn)", 0))
				insert := ean13Held(t, p, c, "INSERT INTO issn_values VALUES($1,$2)")
				read := ean13Held(t, p, c, "SELECT v FROM issn_values WHERE id=$1")
				for _, expression := range []string{ean13Literal(tc.input) + "::issn", "CAST(" + ean13Literal(tc.input) + " AS issn)", "$1::issn", "CAST($1 AS issn)"} {
					var args []any
					if expression[0] == '$' || expression == "CAST($1 AS issn)" {
						args = []any{tc.input}
					}
					s := issnSelect(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"ISSN"}, [][]any{{tc.output}}, nil, args...)
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, s.Query)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				for _, q := range []string{"SELECT $1::issn AS v", "SELECT CAST($1 AS issn) AS v"} {
					s := issnSelect(b, q, []string{"v"}, []string{"ISSN"}, [][]any{{tc.output}}, nil, []byte(tc.input))
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, q)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				run(issnSelect(b, "SELECT ("+ean13Literal(tc.output)+"::issn)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
				run(fixture.Exec("INSERT INTO issn_values VALUES(1,"+ean13Literal(tc.input)+")", 1))
				run(fixture.Exec(insert.SQL, 1, 2, tc.input))
				run(fixture.Exec(insert.SQL, 1, 3, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 4, tc.input)))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 5, tc.input))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 6, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 10, []byte(tc.input))))
				run(fixture.Exec("INSERT INTO issn_values SELECT 7,v FROM issn_values WHERE id=1", 1))
				run(fixture.Exec("INSERT INTO issn_values SELECT 8,(v::text)::issn FROM issn_values WHERE id=1", 1))
				// Explicit text conversion is supported; assignment of an
				// already-typed text expression is not an implicit ISSN cast.
				want := ean13Diagnostic("42804", `column "v" is of type issn but expression is of type text`)
				want.Position = "34"
				want.Hint = "You will need to rewrite or cast the expression."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO issn_values SELECT 9,v::text FROM issn_values WHERE id=1", want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					run(issnSelect(b, "SELECT v FROM issn_values WHERE id=9", []string{"v"}, []string{"ISSN"}, [][]any{}, []bool{true}))
				}
				run(fixture.Exec("INSERT INTO issn_values SELECT 9,CAST(v::text AS issn) FROM issn_values WHERE id=1", 1))
				for id := 1; id <= 10; id++ {
					s := issnSelect(b, read.SQL, []string{"v"}, []string{"ISSN"}, [][]any{{tc.output}}, []bool{true}, id)
					s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var v sql.NullString
						if err := rows.Scan(&v); err != nil {
							t.Fatal(err)
						}
						if !v.Valid || v.String != output {
							t.Fatalf("NullString = %#v, want %q", v, output)
						}
					}
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, read, b.Recorder, s)
				}
				run(issnSelect(b, "SELECT v::text AS v FROM issn_values WHERE id=8", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
			})
		})
	}
	t.Run("null", func(t *testing.T) {
		issnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE issn_values(id integer NOT NULL,v issn)", 0))
			insert := ean13Held(t, p, c, "INSERT INTO issn_values VALUES($1,$2)")
			run(fixture.Exec("INSERT INTO issn_values VALUES(1,NULL)", 1))
			id := 2
			for _, value := range []any{nil, []byte(nil)} {
				for route := range 3 {
					s := fixture.Exec(insert.SQL, 1, id, value)
					switch route {
					case 0:
						run(s)
					case 1:
						run(fixture.Prepared(s))
					case 2:
						fixture.RunStep(t, p.Ctx, insert, b.Recorder, s)
					}
					id++
				}
			}
			read := ean13Held(t, p, c, "SELECT v FROM issn_values ORDER BY id")
			s := issnSelect(b, read.SQL, []string{"v"}, []string{"ISSN"}, [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}, []bool{true})
			s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var v sql.NullString
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				if v.Valid {
					t.Fatalf("NULL scanned as %#v", v)
				}
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, read, b.Recorder, s)
			for _, value := range []any{nil, []byte(nil)} {
				s := issnSelect(b, "SELECT $1::issn AS v", []string{"v"}, []string{"ISSN"}, [][]any{{nil}}, nil, value)
				run(s)
				run(fixture.Prepared(s))
			}
			if !b.PG {
				// Native pre-cancellation is an ordinary Go boundary, not an
				// invented SQLSTATE or a claim about lib/pq admission timing.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, input := range []any{"12345670!", []byte("9771234567000!"), nil, []byte(nil)} {
					if rows, err := c.QueryContext(ctx, "SELECT $1::issn", input); !errors.Is(err, context.Canceled) {
						if rows != nil {
							rows.Close()
						}
						t.Fatalf("pre-canceled input query = %v", err)
					}
				}
				codec := ean13Held(t, p, c, "SELECT $1::issn")
				if rows, err := codec.Statement.QueryContext(ctx, "12345670!"); !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("pre-canceled held codec = %v", err)
				}
				if stmt, err := c.PrepareContext(ctx, "SELECT $1::issn"); !errors.Is(err, context.Canceled) {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatalf("pre-canceled prepare = %v", err)
				}
				issnCancellation(t, p, b, c)
			}
		})
	})
}