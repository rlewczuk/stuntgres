package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryTransformDiagnostics(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	geometryTypes := []string{"point", "line", "lseg", "box", "path", "polygon", "circle"}
	for _, op := range []string{"+", "-", "*", "/"} {
		t.Run(op, func(t *testing.T) {
			geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, left := range geometryTypes {
					for _, right := range geometryTypes {
						admitted := right == "point" && (left == "point" || left == "box" || left == "path" || left == "circle") || op == "+" && left == "path" && right == "path"
						if admitted {
							continue
						}
						q := "SELECT /*é界*/ NULL::" + left + " " + op + " NULL::" + right
						pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, " "+op+" ")+1]))
						for _, prepared := range []bool{false, true} {
							run(boxError(q, "42883", "operator does not exist: "+left+" "+op+" "+right, pos, hint, prepared, false))
						}
					}
				}
				for _, typ := range geometryTypes {
					for _, scalar := range []string{"integer", "numeric"} {
						for _, reverse := range []bool{false, true} {
							left, right := typ, scalar
							if reverse {
								left, right = right, left
							}
							q := "SELECT NULL::" + left + " " + op + " NULL::" + right
							pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, " "+op+" ")+1]))
							for _, prepared := range []bool{false, true} {
								run(boxError(q, "42883", "operator does not exist: "+left+" "+op+" "+right, pos, hint, prepared, false))
							}
						}
					}
					if op == "+" || op == "-" {
						q := "SELECT " + op + " NULL::" + typ
						for _, prepared := range []bool{false, true} {
							run(boxError(q, "42883", "operator does not exist: "+op+" "+typ, "8", "No operator matches the given name and argument type. You might need to add an explicit type cast.", prepared, false))
						}
					}
				}
			})
		})
	}
	t.Run("empty_and_recovery", func(t *testing.T) {
		geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE geom_transform_atomic(id integer,p point,d point)", 0))
			for _, suffix := range []string{"", " FROM geom_transform_atomic", " FROM geom_transform_atomic WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					run(boxError("SELECT NULL::point + NULL::box"+suffix, "42883", "operator does not exist: point + box", "20", hint, prepared, false))
				}
			}
			q := "SELECT $1::point / $2::point AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for range 2 {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxError(q, "22012", "division by zero", "", "", false, true, "(1,2)", "(0,0)"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryTransformSelect(b, q, []string{"v"}, []string{"POINT"}, [][]any{{"(1.5,0.5)"}}, "(1,2)", "(1,1)"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryTransformSelect(b, q, []string{"v"}, []string{"POINT"}, [][]any{{nil}}, nil, "(1,1)"))
			}
			run(fixture.Exec("INSERT INTO geom_transform_atomic VALUES(1,'(1,2)','(1,1)'),(2,'(3,4)','(0,0)')", 2))
			want := [][]any{{int64(1), "(1,2)", "(1,1)"}, {int64(2), "(3,4)", "(0,0)"}}
			for _, prepared := range []bool{false, true} {
				run(boxError("UPDATE geom_transform_atomic SET p=p/d", "22012", "division by zero", "", "", prepared, true))
				run(geometryTransformSelect(b, "SELECT id,p,d FROM geom_transform_atomic ORDER BY id", []string{"id", "p", "d"}, []string{"INT4", "POINT", "POINT"}, want))
				run(boxError("INSERT INTO geom_transform_atomic SELECT id+10,p/d,d FROM geom_transform_atomic ORDER BY id", "22012", "division by zero", "", "", prepared, true))
				run(geometryTransformSelect(b, "SELECT id,p,d FROM geom_transform_atomic ORDER BY id", []string{"id", "p", "d"}, []string{"INT4", "POINT", "POINT"}, want))
			}
			run(fixture.Exec("UPDATE geom_transform_atomic SET p=p/point '(1,1)' WHERE id=1", 1))
			run(geometryTransformSelect(b, "SELECT p AS v FROM geom_transform_atomic WHERE id=1", []string{"v"}, []string{"POINT"}, [][]any{{"(1.5,0.5)"}}))
		})
	})
}
