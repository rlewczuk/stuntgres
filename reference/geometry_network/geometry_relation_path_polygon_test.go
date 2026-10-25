package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryRelationPathPolygon(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const openSquare = "[(0,0),(2,0),(2,2),(0,2)]"
		const notch = "((0,0),(4,0),(4,4),(3,4),(3,1),(1,1),(1,4),(0,4))"
		const triangle = "((0.5,3),(3.5,3),(2,0.5))"
		// Singleton closure expectations are independent release predictions,
		// deliberately exact so oracle discovery cannot accept alternatives.
		geometryRelationEdges(t, p, b, c, []geometryRelationCase{
			{"@>", "path", "point", grA, grR, "BOOL", true},
			{"<->", "point", "path", grR, grA, "FLOAT8", float64(1)},
			{"<->", "path", "point", grA, grR, "FLOAT8", float64(1)},
			{"@>", "path", "point", openSquare, "(0,1)", "BOOL", false},
			{"@>", "path", "point", grA, "(0,1)", "BOOL", true},
			{"@>", "path", "point", "[(1,1)]", grR, "BOOL", false},
			{"<->", "point", "path", grR, "[(1,1)]", "FLOAT8", float64(0)},
			{"<->", "path", "point", "[(1,1)]", "(8,8)", "FLOAT8", float64(0)},
			{"<->", "path", "path", "[(1,1)]", grH, "FLOAT8", nil},
			{"<->", "path", "path", grH, "[(1,1)]", "FLOAT8", nil},
			{"?#", "path", "path", "[(1,1)]", grH, "BOOL", false},
			{"@>", "path", "point", "((1,1))", grR, "BOOL", true},
			{"<->", "point", "path", "(4,5)", "((1,1))", "FLOAT8", float64(5)},
			{"<->", "path", "path", "((1,1))", grH, "FLOAT8", float64(1)},
			{"?#", "path", "path", "((1,1))", grH, "BOOL", false},
			{"?#", "path", "path", grA, grE, "BOOL", false},
			{"&&", "polygon", "polygon", grA, grE, "BOOL", true},
			{"<->", "polygon", "polygon", grA, grE, "FLOAT8", float64(0)},
			{"<->", "point", "polygon", grR, grA, "FLOAT8", float64(0)},
			{"<->", "polygon", "point", grA, grR, "FLOAT8", float64(0)},
			{"<->", "point", "polygon", "(0,1)", grA, "FLOAT8", float64(0)},
			{"<->", "polygon", "point", grA, grQ, "FLOAT8", float64(2)},
			{"@>", "polygon", "point", notch, "(0.5,3)", "BOOL", true},
			{"@>", "polygon", "point", notch, "(3.5,3)", "BOOL", true},
			{"@>", "polygon", "point", notch, "(2,0.5)", "BOOL", true},
			{"@>", "polygon", "polygon", notch, triangle, "BOOL", false},
			{"<@", "polygon", "polygon", triangle, notch, "BOOL", false},
			{"&&", "polygon", "polygon", notch, triangle, "BOOL", true},
			{"<->", "polygon", "polygon", notch, triangle, "FLOAT8", float64(0)},
			{"&&", "polygon", "polygon", notch, "((1.5,2),(2.5,2),(2.5,3),(1.5,3))", "BOOL", false},
			{"@>", "polygon", "polygon", grA, "((0,0),(2,0),(1,1))", "BOOL", true},
			{"@>", "polygon", "polygon", grA, grA, "BOOL", true},
			{"&&", "polygon", "polygon", grA, "((2,0),(4,0),(4,2),(2,2))", "BOOL", true},
			{"<->", "polygon", "polygon", grA, "((2,0),(4,0),(4,2),(2,2))", "FLOAT8", float64(0)},
			{"@>", "polygon", "point", "((1,1))", grR, "BOOL", true},
			{"@>", "polygon", "polygon", grA, "((1,1))", "BOOL", true},
			{"<->", "polygon", "polygon", "((0,0))", "((3,4))", "FLOAT8", float64(5)},
			{"@>", "polygon", "polygon", grA, "((0,0),(0,0),(2,0),(1,1))", "BOOL", true},
		})
	})
}
