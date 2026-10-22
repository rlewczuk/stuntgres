package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePointDiagnostics(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE point_errors(id integer,p point)", 0))
		cases := []struct{ input, code, message string }{}
		for _, input := range []string{"", " ", "bad", "1", "1;2", "1,2,3", "[1,2]", "((1,2))", "(1,2", "1,2)", "(1,2)junk"} {
			cases = append(cases, struct{ input, code, message string }{input, "22P02", "invalid input syntax for type point: \"" + input + "\""})
		}
		for _, tc := range [][2]string{{"1e400,2", "1e400"}, {"1e-4000,2", "1e-4000"}, {"(1e400;2)", "1e400"}} {
			cases = append(cases, struct{ input, code, message string }{tc[0], "22003", "\"" + tc[1] + "\" is out of range for type double precision"})
		}
		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					q := "SELECT point " + boxQuote(tc.input)
					run(boxError(q, tc.code, tc.message, "14", "", prepared, false))
				}
				q := "SELECT $1::point AS p"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Context = "unnamed portal parameter $1 = '...'"
				if tc.input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				for _, arg := range []any{tc.input, []byte(tc.input)} {
					s := fixture.ErrorDiagnostic(q, d, arg)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{"(3,4)"}}, "3,4"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{nil}}, nil))
			})
		}
		for _, q := range []string{"SELECT /*é*/ point 'bad'", "SELECT 'bad'::point", "SELECT CAST('bad' AS point)", "INSERT INTO point_errors VALUES(9,'bad')", "SELECT 'bad'::text::point"} {
			runtime := strings.Contains(q, "::text::")
			pos := ""
			if !runtime {
				pos = fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, "'")]))
			}
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "22P02", "invalid input syntax for type point: \"bad\"", pos, "", prepared, runtime))
			}
		}
		run(fixture.Exec("INSERT INTO point_errors VALUES(1,'1,2')", 1))
		for _, prepared := range []bool{false, true} {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type point: \"bad\"")
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic("INSERT INTO point_errors VALUES(2,$1),(3,$2)", d, "3,4", "bad")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			q := "INSERT INTO point_errors VALUES(2,'3,4'),(3,'bad')"
			run(boxError(q, "22P02", d.Message, fixture.Position(strings.Index(q, "'bad'")+1), "", prepared, false))
			run(boxSelect(b, "SELECT id,p FROM point_errors ORDER BY id", []string{"id", "p"}, []string{"INT4", "POINT"}, [][]any{{int64(1), "(1,2)"}}))
		}
		mismatch := "No operator matches the given name and argument types. You might need to add explicit type casts."
		for _, op := range []string{"=", "<", "<=", ">", ">="} {
			q := "SELECT NULL::point " + op + " NULL::point"
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "42883", "operator does not exist: point "+op+" point", "20", mismatch, prepared, false))
			}
		}
		for _, prepared := range []bool{false, true} {
			run(boxError("SELECT NULL::point ~= 1", "42883", "operator does not exist: point ~= integer", "20", mismatch, prepared, false))
			for _, tc := range []struct{ q, code, message, token, hint string }{
				{"SELECT p FROM point_errors WHERE FALSE ORDER BY p", "42883", "could not identify an ordering operator for type point", "p", "Use an explicit ordering operator or modify the query."},
				{"SELECT DISTINCT p FROM point_errors WHERE FALSE", "42883", "could not identify an equality operator for type point", "DISTINCT p", ""},
				{"SELECT p FROM point_errors WHERE FALSE GROUP BY p", "42883", "could not identify an equality operator for type point", "p", ""},
				{"CREATE INDEX point_errors_idx ON point_errors(p)", "42704", "data type point has no default operator class for access method \"btree\"", "", "You must specify an operator class for the index or define a default operator class for the data type."},
			} {
				pos := ""
				if tc.token != "" {
					index := strings.LastIndex(tc.q, tc.token)
					if tc.token == "DISTINCT p" {
						index += len("DISTINCT ")
					}
					pos = fixture.Position(index + 1)
				}
				run(boxError(tc.q, tc.code, tc.message, pos, tc.hint, prepared, tc.code == "42704"))
			}
		}
	})
}
