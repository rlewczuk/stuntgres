package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Exact fields here are independently source-reasoned discovery candidates.
// The pinned oracle, never native output, resolves any differences.
func TestReferencePolygonDiagnostics(t *testing.T) {
	polygonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(t *testing.T, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(t, fixture.Exec("CREATE TABLE polygon_errors(id integer,v polygon)", 0))
		for _, input := range []string{"", "()", "[(0,0),(1,1)]", "0,0,1", "bad,0", "((0,0),(1,1)", "0,0 junk", "1e999,0", "1e-999,0"} {
			t.Run("input="+input, func(t *testing.T) {
				base := polygonInputDiagnostic(input)
				if strings.HasPrefix(input, "1e") {
					base = fixture.ControlsReferenceDiagnostic("22003", "\""+strings.Split(input, ",")[0]+"\" is out of range for type double precision")
				}
				for _, q := range []string{"SELECT polygon " + boxQuote(input), "SELECT /*é*/ pg_catalog.polygon " + boxQuote(input), "SELECT CAST(" + boxQuote(input) + " AS polygon)", "SELECT " + boxQuote(input) + "::polygon FROM polygon_errors WHERE FALSE"} {
					for _, prepared := range []bool{false, true} {
						d := base
						d.Position = fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, "'")]))
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(t, s)
					}
				}
				q := "SELECT $1::polygon AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, arg := range []any{input, []byte(input)} {
					d := base
					d.Context = "unnamed portal parameter $1 = '...'"
					if input == "" {
						d.Context = "unnamed portal parameter $1 = ''"
					}
					s := fixture.ErrorDiagnostic(q, d, arg)
					s.SelectRows, s.ErrorStage = true, "execute"
					run(t, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, polygonSelect(b, q, []string{"v"}, []string{"POLYGON"}, [][]any{{polygonTriangle}}, polygonTriangle))
				}
			})
		}
		for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
			t.Run("operator="+op, func(t *testing.T) {
				normal := op
				if normal == "!=" {
					normal = "<>"
				}
				for _, operands := range [][2]string{{"NULL::polygon", "NULL::polygon"}, {"v", "v"}} {
					q := "SELECT /*é*/ " + operands[0] + " " + op + " " + operands[1] + " AS v FROM polygon_errors"
					pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, " "+op+" ")+1]))
					for _, prepared := range []bool{false, true} {
						run(t, boxError(q, "42883", "operator does not exist: polygon "+normal+" polygon", pos, "No operator matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
					}
				}
			})
		}
		for _, tc := range []struct{ q, code, message, hint, token string }{
			{"SELECT v FROM polygon_errors ORDER BY v", "42883", "could not identify an ordering operator for type polygon", "Use an explicit ordering operator or modify the query.", "v"},
			{"SELECT DISTINCT v FROM polygon_errors", "42883", "could not identify an equality operator for type polygon", "", "v"},
			{"SELECT v FROM polygon_errors GROUP BY v", "42883", "could not identify an equality operator for type polygon", "", "v"},
			{"SELECT area(v) FROM polygon_errors", "42883", "function area(polygon) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "area"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				index := strings.LastIndex(tc.q, tc.token)
				for _, prepared := range []bool{false, true} {
					run(t, boxError(tc.q, tc.code, tc.message, fixture.Position(index+1), tc.hint, prepared, false))
				}
			})
		}
		t.Run("atomic multirow", func(t *testing.T) {
			run(t, fixture.Exec("INSERT INTO polygon_errors VALUES(1,'0,0')", 1))
			for _, prepared := range []bool{false, true} {
				d := polygonInputDiagnostic("bad")
				d.Context = "unnamed portal parameter $2 = '...'"
				s := fixture.ErrorDiagnostic("INSERT INTO polygon_errors VALUES(2,$1),(3,$2)", d, polygonTriangle, "bad")
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(t, s)
				run(t, polygonSelect(b, "SELECT id,v FROM polygon_errors ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(1), "((0,0))"}}))
			}
			q := "INSERT INTO polygon_errors VALUES(2,'0,0'),(3,'bad')"
			run(t, boxError(q, "22P02", "invalid input syntax for type polygon: \"bad\"", fixture.Position(strings.Index(q, "'bad'")+1), "", false, false))
			run(t, polygonSelect(b, "SELECT id,v FROM polygon_errors ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(1), "((0,0))"}}))
		})
	})
}
