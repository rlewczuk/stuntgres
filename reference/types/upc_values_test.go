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
var upcValues = []struct{ input, output string }{
	{"123456789012", "123456789012"},
	{"0123456789012", "123456789012"},
	{"0-123-45678901-2", "123456789012"},
	{"220356483481", "220356483481"},
	{"0220356483481", "220356483481"},
	{"000000000000", "000000000000"},
	{"0000000000000", "000000000000"},
	{"00000000001?", "000000000017"},
	{"999999999993", "999999999993"},
	{"0999999999993", "999999999993"},
	{"12345678901?", "123456789012"},
	{"123456789010!", "123456789012!"},
	{"0123456789010!", "123456789012!"},
	{"123456789012!", "123456789012!"},
	{"12345678901?!", "123456789012"},
	{"0123456789012 ", "123456789012"},
	{"0123456789012-", "123456789012"},
	{"1 2 3 4-5 6 7 8 9 0 1 2", "123456789012"},
}

func TestReferenceUpcValues(t *testing.T) {
	for i, tc := range upcValues {
		t.Run(fmt.Sprintf("value_%02d", i), func(t *testing.T) {
			upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				output := upcOutput(b, tc.output)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE upc_values(id integer NOT NULL,v upc)", 0))
				insert := ean13Held(t, p, c, "INSERT INTO upc_values VALUES($1,$2)")
				read := ean13Held(t, p, c, "SELECT v FROM upc_values WHERE id=$1")
				for _, expression := range []string{ean13Literal(tc.input) + "::upc", "CAST(" + ean13Literal(tc.input) + " AS upc)", "$1::upc", "CAST($1 AS upc)"} {
					var args []any
					if expression[0] == '$' || expression == "CAST($1 AS upc)" {
						args = []any{tc.input}
					}
					s := upcSelect(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"UPC"}, [][]any{{tc.output}}, nil, args...)
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, s.Query)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				for _, q := range []string{"SELECT $1::upc AS v", "SELECT CAST($1 AS upc) AS v"} {
					s := upcSelect(b, q, []string{"v"}, []string{"UPC"}, [][]any{{tc.output}}, nil, []byte(tc.input))
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, q)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				run(upcSelect(b, "SELECT ("+ean13Literal(tc.output)+"::upc)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
				run(fixture.Exec("INSERT INTO upc_values VALUES(1,"+ean13Literal(tc.input)+")", 1))
				run(fixture.Exec(insert.SQL, 1, 2, tc.input))
				run(fixture.Exec(insert.SQL, 1, 3, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 4, tc.input)))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 5, tc.input))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 6, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 10, []byte(tc.input))))
				run(fixture.Exec("INSERT INTO upc_values SELECT 7,v FROM upc_values WHERE id=1", 1))
				run(fixture.Exec("INSERT INTO upc_values SELECT 8,(v::text)::upc FROM upc_values WHERE id=1", 1))
				// Explicit text conversion is supported; assignment of an
				// already-typed text expression is not an implicit UPC cast.
				want := ean13Diagnostic("42804", `column "v" is of type upc but expression is of type text`)
				want.Position = "33"
				want.Hint = "You will need to rewrite or cast the expression."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO upc_values SELECT 9,v::text FROM upc_values WHERE id=1", want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					run(upcSelect(b, "SELECT v FROM upc_values WHERE id=9", []string{"v"}, []string{"UPC"}, [][]any{}, []bool{true}))
				}
				run(fixture.Exec("INSERT INTO upc_values SELECT 9,CAST(v::text AS upc) FROM upc_values WHERE id=1", 1))
				for id := 1; id <= 10; id++ {
					s := upcSelect(b, read.SQL, []string{"v"}, []string{"UPC"}, [][]any{{tc.output}}, []bool{true}, id)
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
				run(upcSelect(b, "SELECT v::text AS v FROM upc_values WHERE id=8", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
			})
		})
	}
	t.Run("null", func(t *testing.T) {
		upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE upc_values(id integer NOT NULL,v upc)", 0))
			insert := ean13Held(t, p, c, "INSERT INTO upc_values VALUES($1,$2)")
			run(fixture.Exec("INSERT INTO upc_values VALUES(1,NULL)", 1))
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
			read := ean13Held(t, p, c, "SELECT v FROM upc_values ORDER BY id")
			s := upcSelect(b, read.SQL, []string{"v"}, []string{"UPC"}, [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}, []bool{true})
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
				s := upcSelect(b, "SELECT $1::upc AS v", []string{"v"}, []string{"UPC"}, [][]any{{nil}}, nil, value)
				run(s)
				run(fixture.Prepared(s))
			}
			if !b.PG {
				// Native pre-cancellation is an ordinary Go boundary, not an
				// invented SQLSTATE or a claim about lib/pq admission timing.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, input := range []any{"123456789010!", []byte("0123456789010!"), nil, []byte(nil)} {
					if rows, err := c.QueryContext(ctx, "SELECT $1::upc", input); !errors.Is(err, context.Canceled) {
						if rows != nil {
							rows.Close()
						}
						t.Fatalf("pre-canceled input query = %v", err)
					}
				}
				codec := ean13Held(t, p, c, "SELECT $1::upc")
				if rows, err := codec.Statement.QueryContext(ctx, "123456789010!"); !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("pre-canceled held codec = %v", err)
				}
				if stmt, err := c.PrepareContext(ctx, "SELECT $1::upc"); !errors.Is(err, context.Canceled) {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatalf("pre-canceled prepare = %v", err)
				}
			}
		})
	})
}