package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

const geometryPositionHint = "No operator matches the given name and argument types. You might need to add explicit type casts."

func TestReferenceGeometryPositionDiagnostics(t *testing.T) {
	for _, left := range []string{"point", "box", "polygon", "circle", "integer", "text"} {
		for _, right := range []string{"point", "box", "polygon", "circle", "integer", "text"} {
			if (left == "integer" || left == "text") && (right == "integer" || right == "text") {
				continue
			}
			t.Run(left+"_"+right, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
						run(fixture.Exec("CREATE TABLE position_errors(id integer)", 0))
						for _, direction := range geometryPositionDirections {
							if left == right && geometryPositionAdmitted(left, direction.op) {
								continue
							}
							for _, prefix := range []string{"SELECT ", "SELECT /*é界*/ "} {
								before := prefix + "NULL::" + left + " "
								pos := fixture.Position(1 + utf8.RuneCountInString(before))
								for _, suffix := range []string{"", " FROM position_errors WHERE FALSE"} {
									q := before + direction.op + " NULL::" + right + suffix
									for _, prepared := range []bool{false, true} {
										run(boxError(q, "42883", "operator does not exist: "+left+" "+direction.op+" "+right, pos, geometryPositionHint, prepared, false))
									}
								}
							}
						}
					})
				}
			})
		}
	}
	// Valid held statements fail only on a malformed runtime datum and remain
	// usable after that failure; invalid overloads above cannot be prepared.
	for _, shape := range geometryPositionShapes[1:3] {
		for _, op := range []string{"<<", "<<|"} {
			t.Run(shape.typ+"_"+op+"_held_recovery", func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						c, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						q := "SELECT $1::" + shape.typ + " " + op + " $2::" + shape.typ + " AS v"
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						for _, index := range []int{1, 2} {
							args := []any{shape.a, shape.b}
							args[index-1] = "bad"
							d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+shape.typ+": \"bad\"")
							d.Context = "unnamed portal parameter $" + fixture.Position(index) + " = '...'"
							s := fixture.ErrorDiagnostic(q, d, args...)
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
							for _, recovery := range []struct {
								a    any
								want any
							}{{shape.a, true}, {nil, nil}, {[]byte(nil), nil}, {[]byte(shape.a), true}} {
								fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{recovery.want}}, recovery.a, shape.b))
							}
						}
					})
				}
			})
		}
	}
}
