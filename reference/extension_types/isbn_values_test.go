package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The first four examples are published REL_18_6 regression outputs. The
// remaining fixtures are independently derived from that release's ISBN tables
// and checksum algorithm, not generated from the native implementation.
var isbnValues = []struct{ input, output string }{
	{"9780123456786", "0-12-345678-9"},
	{"123456789X", "1-234-56789-X"},
	{"9791234567896", "979-123456789-6"},
	{"978-0-393-04002-9", "0-393-04002-X"},
	{"123456789x", "1-234-56789-X"},
	{"123456789?", "1-234-56789-X"},
	{"978012345678?", "0-12-345678-9"},
	{"1234567890!", "1-234-56789-X!"},
	{"123456789X!", "1-234-56789-X!"},
	{"9780123456780!", "0-12-345678-9!"},
	{"123456789?!", "1-234-56789-X"},
	{"0000000000", "0-00-000000-0"},
	{"9780200000000", "0-200-00000-4"},
	{"9780700000005", "0-7000-0000-3"},
	{"9780950000008", "0-9500000-0-0"},
	{"9791000000008", "979-10-00-00000-8"},
	{"1 234-56789X", "1-234-56789-X"},
}

func TestReferenceIsbnValues(t *testing.T) {
	for i, tc := range isbnValues {
		t.Run(fmt.Sprintf("value_%02d", i), func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE isbn_values(id integer NOT NULL,v isbn)", 0))
				insert := ean13Held(t, p, c, "INSERT INTO isbn_values VALUES($1,$2)")
				read := ean13Held(t, p, c, "SELECT v FROM isbn_values WHERE id=$1")
				for _, expression := range []string{ean13Literal(tc.input) + "::isbn", "CAST(" + ean13Literal(tc.input) + " AS isbn)", "$1::isbn", "CAST($1 AS isbn)"} {
					var args []any
					if expression[0] == '$' || expression == "CAST($1 AS isbn)" {
						args = []any{tc.input}
					}
					s := fixture.Ean13Select(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"ISBN"}, [][]any{{tc.output}}, nil, args...)
					run(s)
					run(fixture.Prepared(s))
				}
				run(fixture.Exec("INSERT INTO isbn_values VALUES(1,"+ean13Literal(tc.input)+")", 1))
				run(fixture.Exec(insert.SQL, 1, 2, tc.input))
				run(fixture.Exec(insert.SQL, 1, 3, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 4, tc.input)))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 5, tc.input))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 6, []byte(tc.input)))
				run(fixture.Exec("INSERT INTO isbn_values SELECT 7,v FROM isbn_values WHERE id=1", 1))
				run(fixture.Exec("INSERT INTO isbn_values SELECT 8,(v::text)::isbn FROM isbn_values WHERE id=1", 1))
				// Explicit text conversion is supported; assignment of an
				// already-typed text expression is not an implicit ISBN cast.
				want := ean13Diagnostic("42804", `column "v" is of type isbn but expression is of type text`)
				want.Position = "34"
				want.Hint = "You will need to rewrite or cast the expression."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO isbn_values SELECT 9,v::text FROM isbn_values WHERE id=1", want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Ean13Select(b, "SELECT v FROM isbn_values WHERE id=9", []string{"v"}, []string{"ISBN"}, [][]any{}, []bool{true}))
				}
				run(fixture.Exec("INSERT INTO isbn_values SELECT 9,CAST(v::text AS isbn) FROM isbn_values WHERE id=1", 1))
				for id := 1; id <= 9; id++ {
					s := fixture.Ean13Select(b, read.SQL, []string{"v"}, []string{"ISBN"}, [][]any{{tc.output}}, []bool{true}, id)
					s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var v sql.NullString
						if err := rows.Scan(&v); err != nil {
							t.Fatal(err)
						}
						if !v.Valid || v.String != tc.output {
							t.Fatalf("NullString = %#v, want %q", v, tc.output)
						}
					}
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, read, b.Recorder, s)
				}
				run(fixture.Select("SELECT v::text AS v FROM isbn_values WHERE id=8", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}))
			})
		})
	}
	t.Run("null", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE isbn_values(id integer NOT NULL,v isbn)", 0))
			insert := ean13Held(t, p, c, "INSERT INTO isbn_values VALUES($1,$2)")
			run(fixture.Exec("INSERT INTO isbn_values VALUES(1,NULL)", 1))
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
			read := ean13Held(t, p, c, "SELECT v FROM isbn_values ORDER BY id")
			s := fixture.Ean13Select(b, read.SQL, []string{"v"}, []string{"ISBN"}, [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}, []bool{true})
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
				s := fixture.Ean13Select(b, "SELECT $1::isbn AS v", []string{"v"}, []string{"ISBN"}, [][]any{{nil}}, nil, value)
				run(s)
				run(fixture.Prepared(s))
			}
			if !b.PG {
				// Native pre-cancellation is an ordinary Go boundary, not an
				// invented SQLSTATE or a claim about lib/pq admission timing.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, input := range []any{"1234567890!", []byte("9780123456780!"), nil, []byte(nil)} {
					if rows, err := c.QueryContext(ctx, "SELECT $1::isbn", input); !errors.Is(err, context.Canceled) {
						if rows != nil {
							rows.Close()
						}
						t.Fatalf("pre-canceled input query = %v", err)
					}
				}
				codec := ean13Held(t, p, c, "SELECT $1::isbn")
				if rows, err := codec.Statement.QueryContext(ctx, "1234567890!"); !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("pre-canceled held codec = %v", err)
				}
				if stmt, err := c.PrepareContext(ctx, "SELECT $1::isbn"); !errors.Is(err, context.Canceled) {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatalf("pre-canceled prepare = %v", err)
				}
			}
		})
	})
}
