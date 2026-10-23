package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Table 9.36 and REL_18_6 geo_ops.c specify these independently of
// native admission. The two Boolean columns describe A op B and A op A.
var geometryPositionDirections = []struct {
	op            string
	forward, self bool
}{
	{"<<", true, false}, {">>", false, false},
	{"&<", true, true}, {"&>", false, true},
	{"<<|", true, false}, {"|>>", false, false},
	{"&<|", true, true}, {"|&>", false, true},
	{"<^", true, false}, {">^", false, false},
}

var geometryPositionShapes = []struct{ typ, a, b string }{
	{"point", "(0,0)", "(3,3)"},
	{"box", "(1,1),(0,0)", "(4,4),(3,3)"},
	{"polygon", "((0,0),(1,0),(1,1),(0,1))", "((3,3),(4,3),(4,4),(3,4))"},
	{"circle", "<(0,0),0.5>", "<(3,3),0.5>"},
}

func geometryPositionAdmitted(typ, op string) bool {
	if typ == "point" {
		return op == "<<" || op == ">>" || op == "<<|" || op == "|>>" || op == "<^" || op == ">^"
	}
	return typ == "box" || op != "<^" && op != ">^"
}

func TestReferenceGeometryPositionMatrix(t *testing.T) {
	for _, shape := range geometryPositionShapes {
		for _, direction := range geometryPositionDirections {
			if !geometryPositionAdmitted(shape.typ, direction.op) {
				continue
			}
			t.Run(shape.typ+"_"+direction.op, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						c, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
						for _, pair := range []struct {
							a, b string
							want bool
						}{
							{shape.a, shape.b, direction.forward}, {shape.b, shape.a, !direction.forward}, {shape.a, shape.a, direction.self},
						} {
							geometryRelationWitness(t, p, backend, c, geometryRelationCase{direction.op, shape.typ, shape.typ, pair.a, pair.b, "BOOL", pair.want})
						}
						run(fixture.Exec("CREATE TABLE position_matrix(a "+shape.typ+",b "+shape.typ+")", 0))
						run(fixture.Exec("INSERT INTO position_matrix VALUES($1,$2),(NULL,$2),($1,NULL),(NULL,NULL)", 4, shape.a, shape.b))
						s := fixture.Select("SELECT a "+direction.op+" b AS v FROM position_matrix", []string{"v"}, []string{"BOOL"}, [][]any{{direction.forward}, {nil}, {nil}, {nil}})
						s.Bag = true
						run(s)
						run(fixture.Prepared(s))
						for _, expr := range []string{
							fmt.Sprintf("%s %s OPERATOR(pg_catalog.%s) %s %s", shape.typ, boxQuote(shape.a), direction.op, shape.typ, boxQuote(shape.b)),
							shape.typ + " " + boxQuote(shape.a) + " " + direction.op + " " + boxQuote(shape.b),
							boxQuote(shape.a) + " " + direction.op + " " + shape.typ + " " + boxQuote(shape.b),
						} {
							s := fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{direction.forward}})
							run(s)
							run(fixture.Prepared(s))
						}
					})
				}
			})
		}
	}
}
