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
var issn13Values = []struct{ input, output string }{
	{"12345679", "977-1234-567-00-3"},
	{"1234-5679", "977-1234-567-00-3"},
	{"9771234567003", "977-1234-567-00-3"},
	{"9771234567898", "977-1234-567-89-8"},
	{"1436-4522", "977-1436-452-00-8"},
	{"1144875x", "977-1144-875-00-7"},
	{"1144875X", "977-1144-875-00-7"},
	{"1144875?", "977-1144-875-00-7"},
	{"3251231?", "977-3251-231-00-4"},
	{"00000000", "977-0000-000-00-3"},
	{"99999994", "977-9999-999-00-8"},
	{"12345679 ", "977-1234-567-00-3"},
	{"12345679-", "977-1234-567-00-3"},
	{"1 2 3 4-5 6 7 9", "977-1234-567-00-3"},
	{"12345670!", "977-1234-567-00-3!"},
	{"12345679!", "977-1234-567-00-3!"},
	{"9771234567000!", "977-1234-567-00-3!"},
	{"1234567?!", "977-1234-567-00-3"},
	{"9771234567898!", "977-1234-567-89-8!"},
}

func TestReferenceIssn13Values(t *testing.T) {
	for i, tc := range issn13Values {
		t.Run(fmt.Sprintf("value_%02d", i), func(t *testing.T) {
			issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				output := tc.output
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE issn13_values(id integer NOT NULL,v issn13)", 0))
				insert := ean13Held(t, p, c, "INSERT INTO issn13_values VALUES($1,$2)")
				read := ean13Held(t, p, c, "SELECT v FROM issn13_values WHERE id=$1")
				for _, expression := range []string{ean13Literal(tc.input) + "::issn13", "CAST(" + ean13Literal(tc.input) + " AS issn13)", "$1::issn13", "CAST($1 AS issn13)"} {
					var args []any
					if expression[0] == '$' || expression == "CAST($1 AS issn13)" {
						args = []any{tc.input}
					}
					s := ean13Select(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"ISSN13"}, [][]any{{tc.output}}, nil, args...)
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, s.Query)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				for _, q := range []string{"SELECT $1::issn13 AS v", "SELECT CAST($1 AS issn13) AS v"} {
					s := ean13Select(b, q, []string{"v"}, []string{"ISSN13"}, [][]any{{tc.output}}, nil, []byte(tc.input))
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, q)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				run(ean13Select(b, "SELECT ("+ean13Literal(tc.output)+"::issn13)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
				run(fixture.Exec("INSERT INTO issn13_values VALUES(1,"+ean13Literal(tc.input)+")", 1))
				run(fixture.Exec(insert.SQL, 1, 2, tc.input))
				run(fixture.Exec(insert.SQL, 1, 3, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 4, tc.input)))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 5, tc.input))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 6, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 10, []byte(tc.input))))
				run(fixture.Exec("INSERT INTO issn13_values SELECT 7,v FROM issn13_values WHERE id=1", 1))
				run(fixture.Exec("INSERT INTO issn13_values SELECT 8,(v::text)::issn13 FROM issn13_values WHERE id=1", 1))
				// Explicit text conversion is supported; assignment of an
				// already-typed text expression is not an implicit ISSN cast.
				want := ean13Diagnostic("42804", `column "v" is of type issn13 but expression is of type text`)
				want.Position = "36"
				want.Hint = "You will need to rewrite or cast the expression."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO issn13_values SELECT 9,v::text FROM issn13_values WHERE id=1", want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					run(ean13Select(b, "SELECT v FROM issn13_values WHERE id=9", []string{"v"}, []string{"ISSN13"}, [][]any{}, []bool{true}))
				}
				run(fixture.Exec("INSERT INTO issn13_values SELECT 9,CAST(v::text AS issn13) FROM issn13_values WHERE id=1", 1))
				for id := 1; id <= 10; id++ {
					s := ean13Select(b, read.SQL, []string{"v"}, []string{"ISSN13"}, [][]any{{tc.output}}, []bool{true}, id)
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
				run(ean13Select(b, "SELECT v::text AS v FROM issn13_values WHERE id=8", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
			})
		})
	}
	t.Run("null", func(t *testing.T) {
		issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE issn13_values(id integer NOT NULL,v issn13)", 0))
			insert := ean13Held(t, p, c, "INSERT INTO issn13_values VALUES($1,$2)")
			run(fixture.Exec("INSERT INTO issn13_values VALUES(1,NULL)", 1))
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
			read := ean13Held(t, p, c, "SELECT v FROM issn13_values ORDER BY id")
			s := ean13Select(b, read.SQL, []string{"v"}, []string{"ISSN13"}, [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}, []bool{true})
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
				s := ean13Select(b, "SELECT $1::issn13 AS v", []string{"v"}, []string{"ISSN13"}, [][]any{{nil}}, nil, value)
				run(s)
				run(fixture.Prepared(s))
			}
			if !b.PG {
				// Native pre-cancellation is an ordinary Go boundary, not an
				// invented SQLSTATE or a claim about lib/pq admission timing.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, input := range []any{"12345670!", []byte("9771234567000!"), nil, []byte(nil)} {
					if rows, err := c.QueryContext(ctx, "SELECT $1::issn13", input); !errors.Is(err, context.Canceled) {
						if rows != nil {
							rows.Close()
						}
						t.Fatalf("pre-canceled input query = %v", err)
					}
				}
				codec := ean13Held(t, p, c, "SELECT $1::issn13")
				if rows, err := codec.Statement.QueryContext(ctx, "12345670!"); !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("pre-canceled held codec = %v", err)
				}
				if stmt, err := c.PrepareContext(ctx, "SELECT $1::issn13"); !errors.Is(err, context.Canceled) {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatalf("pre-canceled prepare = %v", err)
				}
			}
		})
	})
}