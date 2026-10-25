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
var ismn13Values = []struct{ input, output string }{
	{"9790123456785", "979-0-1234-5678-5"},
	{"979-0-1234-5678-5", "979-0-1234-5678-5"},
	{"M123456785", "979-0-1234-5678-5"},
	{"m-1234-5678-5", "979-0-1234-5678-5"},
	{"M-3452-4680-5", "979-0-3452-4680-5"},
	{"9790345246805", "979-0-3452-4680-5"},
	{"M12345678?", "979-0-1234-5678-5"},
	{"979012345678?", "979-0-1234-5678-5"},
	{"M123456780!", "979-0-1234-5678-5!"},
	{"9790123456780!", "979-0-1234-5678-5!"},
	{"M123456785!", "979-0-1234-5678-5!"},
	{"M12345678X!", "979-0-1234-5678-5!"},
	{"M12345678?!", "979-0-1234-5678-5"},
	{"979012345678?!", "979-0-1234-5678-5"},
	{" M 1234-5678-5 -", "979-0-1234-5678-5"},
	{"9790123456785 -", "979-0-1234-5678-5"},
	{"M000000001", "979-0-000-00000-1"},
	{"9790000000001", "979-0-000-00000-1"},
	{"M099999996", "979-0-099-99999-6"},
	{"M100000000", "979-0-1000-0000-0"},
	{"M399999993", "979-0-3999-9999-3"},
	{"M400000007", "979-0-40000-000-7"},
	{"M699999990", "979-0-69999-999-0"},
	{"M700000004", "979-0-700000-00-4"},
	{"M899999998", "979-0-899999-99-8"},
	{"M900000002", "979-0-9000000-0-2"},
	{"M999999997", "979-0-9999999-9-7"},
	{"9790999999997", "979-0-9999999-9-7"},
}

func TestReferenceIsmn13Values(t *testing.T) {
	for i, tc := range ismn13Values {
		t.Run(fmt.Sprintf("value_%02d", i), func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE ismn13_values(id integer NOT NULL,v ismn13)", 0))
				insert := ean13Held(t, p, c, "INSERT INTO ismn13_values VALUES($1,$2)")
				read := ean13Held(t, p, c, "SELECT v FROM ismn13_values WHERE id=$1")
				for _, expression := range []string{ean13Literal(tc.input) + "::ismn13", "CAST(" + ean13Literal(tc.input) + " AS ismn13)", "$1::ismn13", "CAST($1 AS ismn13)"} {
					var args []any
					if expression[0] == '$' || expression == "CAST($1 AS ismn13)" {
						args = []any{tc.input}
					}
					s := fixture.Ean13Select(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"ISMN13"}, [][]any{{tc.output}}, nil, args...)
					run(s)
					run(fixture.Prepared(s))
					heldCast := ean13Held(t, p, c, s.Query)
					fixture.RunStep(t, p.Ctx, heldCast, b.Recorder, s)
				}
				run(fixture.Ean13Select(b, "SELECT ("+ean13Literal(tc.output)+"::ismn13)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, nil))
				run(fixture.Exec("INSERT INTO ismn13_values VALUES(1,"+ean13Literal(tc.input)+")", 1))
				run(fixture.Exec(insert.SQL, 1, 2, tc.input))
				run(fixture.Exec(insert.SQL, 1, 3, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec(insert.SQL, 1, 4, tc.input)))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 5, tc.input))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 6, []byte(tc.input)))
				run(fixture.Exec("INSERT INTO ismn13_values SELECT 7,v FROM ismn13_values WHERE id=1", 1))
				run(fixture.Exec("INSERT INTO ismn13_values SELECT 8,(v::text)::ismn13 FROM ismn13_values WHERE id=1", 1))
				// Explicit text conversion is supported; assignment of an
				// already-typed text expression is not an implicit ISBN cast.
				want := ean13Diagnostic("42804", `column "v" is of type ismn13 but expression is of type text`)
				want.Position = "36"
				want.Hint = "You will need to rewrite or cast the expression."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO ismn13_values SELECT 9,v::text FROM ismn13_values WHERE id=1", want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Ean13Select(b, "SELECT v FROM ismn13_values WHERE id=9", []string{"v"}, []string{"ISMN13"}, [][]any{}, []bool{true}))
				}
				run(fixture.Exec("INSERT INTO ismn13_values SELECT 9,CAST(v::text AS ismn13) FROM ismn13_values WHERE id=1", 1))
				for id := 1; id <= 9; id++ {
					s := fixture.Ean13Select(b, read.SQL, []string{"v"}, []string{"ISMN13"}, [][]any{{tc.output}}, []bool{true}, id)
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
				run(fixture.Select("SELECT v::text AS v FROM ismn13_values WHERE id=8", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}))
			})
		})
	}
	t.Run("null", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE ismn13_values(id integer NOT NULL,v ismn13)", 0))
			insert := ean13Held(t, p, c, "INSERT INTO ismn13_values VALUES($1,$2)")
			run(fixture.Exec("INSERT INTO ismn13_values VALUES(1,NULL)", 1))
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
			read := ean13Held(t, p, c, "SELECT v FROM ismn13_values ORDER BY id")
			s := fixture.Ean13Select(b, read.SQL, []string{"v"}, []string{"ISMN13"}, [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}, []bool{true})
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
				s := fixture.Ean13Select(b, "SELECT $1::ismn13 AS v", []string{"v"}, []string{"ISMN13"}, [][]any{{nil}}, nil, value)
				run(s)
				run(fixture.Prepared(s))
			}
			if !b.PG {
				// Native pre-cancellation is an ordinary Go boundary, not an
				// invented SQLSTATE or a claim about lib/pq admission timing.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, input := range []any{"M123456780!", []byte("9790123456780!"), nil, []byte(nil)} {
					if rows, err := c.QueryContext(ctx, "SELECT $1::ismn13", input); !errors.Is(err, context.Canceled) {
						if rows != nil {
							rows.Close()
						}
						t.Fatalf("pre-canceled input query = %v", err)
					}
				}
				codec := ean13Held(t, p, c, "SELECT $1::ismn13")
				if rows, err := codec.Statement.QueryContext(ctx, "M123456780!"); !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("pre-canceled held codec = %v", err)
				}
				if stmt, err := c.PrepareContext(ctx, "SELECT $1::ismn13"); !errors.Is(err, context.Canceled) {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatalf("pre-canceled prepare = %v", err)
				}
				ismn13Cancellation(t, p, b, c)
			}
		})
	})
}
