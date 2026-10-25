package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryAlignmentDiagnostics(t *testing.T) {
	geometryTypes := []string{"point", "line", "lseg", "box", "path", "polygon", "circle"}
	for _, op := range []string{"?-", "?|", "?-|", "?||", "~="} {
		t.Run(op, func(t *testing.T) {
			geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				const binaryHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				const unaryHint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
				for _, left := range geometryTypes {
					if (op == "?-" || op == "?|") && left != "line" && left != "lseg" {
						q := "SELECT /*é界*/ " + op + " NULL::" + left
						pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, op)]))
						for _, prepared := range []bool{false, true} {
							run(boxError(q, "42883", "operator does not exist: "+op+" "+left, pos, unaryHint, prepared, false))
						}
					}
					for _, right := range geometryTypes {
						admitted := false
						switch op {
						case "?-", "?|":
							admitted = left == "point" && right == "point"
						case "?-|", "?||":
							admitted = left == right && (left == "line" || left == "lseg")
						case "~=":
							admitted = left == right && (left == "point" || left == "box" || left == "polygon" || left == "circle")
						}
						if admitted {
							continue
						}
						q := "SELECT /*é界*/ NULL::" + left + " " + op + " NULL::" + right
						pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, op)]))
						for _, prepared := range []bool{false, true} {
							run(boxError(q, "42883", "operator does not exist: "+left+" "+op+" "+right, pos, binaryHint, prepared, false))
						}
					}
				}
			})
		})
	}
	t.Run("scalar_and_empty_analysis", func(t *testing.T) {
		geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE geom_alignment_empty(l line,s lseg)", 0))
			for _, tc := range []struct{ left, op, right string }{
				{"point", "=", "point"}, {"point", "<", "point"}, {"point", "<=", "point"}, {"point", ">", "point"}, {"point", ">=", "point"},
				{"polygon", "=", "polygon"}, {"polygon", "<>", "polygon"}, {"polygon", "<", "polygon"}, {"polygon", "<=", "polygon"}, {"polygon", ">", "polygon"}, {"polygon", ">=", "polygon"},
				{"box", "<>", "box"}, {"box", "!=", "box"},
				{"box", "=", "circle"}, {"box", "<>", "circle"}, {"box", "<", "circle"}, {"box", "<=", "circle"}, {"box", ">", "circle"}, {"box", ">=", "circle"},
				{"circle", "=", "box"}, {"circle", "<>", "box"}, {"circle", "<", "box"}, {"circle", "<=", "box"}, {"circle", ">", "box"}, {"circle", ">=", "box"},
				{"line", "?||", "lseg"},
			} {
				for _, suffix := range []string{"", " FROM geom_alignment_empty", " FROM geom_alignment_empty WHERE FALSE"} {
					q := "SELECT /*é界*/ NULL::" + tc.left + " " + tc.op + " NULL::" + tc.right + suffix
					pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, tc.op)]))
					op := tc.op
					if op == "!=" {
						op = "<>"
					}
					for _, prepared := range []bool{false, true} {
						run(boxError(q, "42883", "operator does not exist: "+tc.left+" "+op+" "+tc.right, pos, "No operator matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
					}
				}
			}
		})
	})
	t.Run("arithmetic_and_recovery", func(t *testing.T) {
		geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, tc := range []struct{ typ, op, a, z, valid string }{
				{"line", "?||", "{1,1,1e308}", "{1,-1,1e308}", "{1,1,0}"},
				{"line", "?-|", "{1e200,1,0}", "{1e200,1,0}", "{1,1,0}"},
				{"lseg", "?||", "[(1e308,1),(-1e308,2)]", "[(0,0),(1,1)]", "[(0,0),(1,1)]"},
			} {
				q := "SELECT $1::" + tc.typ + " " + tc.op + " $2::" + tc.typ + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := boxError(q, "22003", "value out of range: overflow", "", "", false, true, tc.a, tc.z)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				literal := "SELECT " + boxQuote(tc.a) + "::" + tc.typ + " " + tc.op + " " + boxQuote(tc.z) + "::" + tc.typ
				for _, prepared := range []bool{false, true} {
					run(boxError(literal, "22003", "value out of range: overflow", "", "", prepared, true))
				}
				want := tc.op == "?||"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, tc.valid, tc.valid))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, nil, tc.valid))
				stmt.Close()
			}
			for _, tc := range []struct{ typ, op, valid string }{{"line", "?-", "{0,1,0}"}, {"lseg", "?|", "[(0,0),(0,1)]"}} {
				q := "SELECT " + tc.op + " $1::" + tc.typ + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+tc.typ+": \"bad\"")
				d.Context = "unnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic(q, d, "bad")
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, tc.valid))
				stmt.Close()
			}
			for _, typ := range []string{"line", "lseg"} {
				valid := "{1,1,0}"
				if typ == "lseg" {
					valid = "[(0,0),(1,1)]"
				}
				q := "SELECT $1::" + typ + " ?|| $2::" + typ + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for i := range 2 {
					args := []any{valid, valid}
					args[i] = "bad"
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+typ+": \"bad\"")
					d.Context = "unnamed portal parameter $" + fixture.Position(i+1) + " = '...'"
					s := fixture.ErrorDiagnostic(q, d, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, valid, valid))
				}
				stmt.Close()
			}
			q := "SELECT '{\"a\":1}'::jsonb ?| ARRAY['a','b'] AS v"
			s := boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			run(fixture.Prepared(s))
		})
	})
}
