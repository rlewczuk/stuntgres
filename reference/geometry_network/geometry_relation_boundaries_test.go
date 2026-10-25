package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryRelationEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, cases []geometryRelationCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(geometryRelationExpr(tc), func(t *testing.T) {
			geometryRelationWitness(t, p, b, c, tc)
		})
	}
}

func TestReferenceGeometryRelationBoundaries(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Every ordered Boolean signature has a false witness, independent of
		// its true matrix witness. Containment and intersection are not synonyms.
		geometryRelationEdges(t, p, b, c, []geometryRelationCase{
			{"@>", "box", "point", grB, grQ, "BOOL", false},
			{"@>", "box", "box", grB, "(6,2),(4,0)", "BOOL", false},
			{"@>", "path", "point", grH, grR, "BOOL", false},
			{"@>", "polygon", "point", grA, grQ, "BOOL", false},
			{"@>", "polygon", "polygon", grA, grD, "BOOL", false},
			{"@>", "circle", "point", grO, grQ, "BOOL", false},
			{"@>", "circle", "circle", grG, grN, "BOOL", false},
			{"<@", "point", "box", grQ, grB, "BOOL", false},
			{"<@", "point", "lseg", grR, grS, "BOOL", false},
			{"<@", "point", "line", grR, grL, "BOOL", false},
			{"<@", "point", "path", grR, grH, "BOOL", false},
			{"<@", "point", "polygon", grQ, grA, "BOOL", false},
			{"<@", "point", "circle", grQ, grO, "BOOL", false},
			{"<@", "box", "box", "(6,2),(4,0)", grB, "BOOL", false},
			{"<@", "lseg", "box", grU, grB, "BOOL", false},
			{"<@", "lseg", "line", grU, grL, "BOOL", false},
			{"<@", "polygon", "polygon", grD, grA, "BOOL", false},
			{"<@", "circle", "circle", grN, grG, "BOOL", false},
			{"&&", "box", "box", grB, "(6,2),(4,0)", "BOOL", false},
			{"&&", "polygon", "polygon", grA, grD, "BOOL", false},
			{"&&", "circle", "circle", grO, grN, "BOOL", false},
			{"?#", "box", "box", grB, "(6,2),(4,0)", "BOOL", false},
			{"?#", "lseg", "box", grU, grB, "BOOL", false},
			{"?#", "lseg", "lseg", grS, grU, "BOOL", false},
			{"?#", "lseg", "line", grU, grL, "BOOL", false},
			{"?#", "line", "box", "{0,1,-3}", grB, "BOOL", false},
			{"?#", "line", "line", grL, "{0,1,-3}", "BOOL", false},
			{"?#", "path", "path", grH, "[(0,3),(2,3)]", "BOOL", false},
			{"#", "box", "box", grB, "(6,2),(4,0)", "BOX", nil},
			{"#", "box", "box", grB, "(4,2),(2,0)", "BOX", "(2,2),(2,0)"},
			{"&&", "box", "box", grB, "(4,2),(2,0)", "BOOL", true},
			{"?#", "box", "box", grB, "(4,2),(2,0)", "BOOL", true},
			{"#", "box", "box", grB, "(4,2),(2.0000005,0)", "BOX", "(2,2),(2.0000005,0)"},
			{"&&", "box", "box", grB, "(4,2),(2.0000005,0)", "BOOL", true},
			{"?#", "box", "box", grB, "(4,2),(2.0000005,0)", "BOOL", true},
			{"#", "box", "box", grB, "(4,2),(2.000002,0)", "BOX", nil},
			{"&&", "box", "box", grB, "(4,2),(2.000002,0)", "BOOL", false},
			{"?#", "box", "box", grB, "(4,2),(2.000002,0)", "BOOL", false},
			{"<@", "point", "box", "(2.0000005,1)", grB, "BOOL", false},
			{"@>", "box", "box", grB, "(2.0000005,1),(1,0)", "BOOL", true},
			{"<@", "point", "box", "(2,2)", grB, "BOOL", true},
			{"@>", "circle", "point", grO, "(1.0000005,0)", "BOOL", false},
			{"<@", "point", "circle", "(1,0)", grO, "BOOL", true},
			{"&&", "circle", "circle", grO, "<(2,0),1>", "BOOL", true},
			{"&&", "circle", "circle", grO, "<(2.0000005,0),1>", "BOOL", true},
			{"&&", "circle", "circle", grO, "<(2.000002,0),1>", "BOOL", false},
			{"@>", "circle", "circle", grG, "<(2,0),1>", "BOOL", true},
			{"<@", "point", "line", "(0,0.0000005)", grL, "BOOL", true},
			{"<@", "point", "line", "(0,0.0000005)", "{0,100,0}", "BOOL", false},
			{"#", "lseg", "lseg", grS, "[(1,0),(3,0)]", "POINT", nil},
			{"?#", "lseg", "lseg", grS, "[(1,0),(3,0)]", "BOOL", false},
			{"<->", "lseg", "lseg", grS, "[(1,0),(3,0)]", "FLOAT8", float64(0)},
			{"#", "lseg", "lseg", grS, "[(2,0),(2,2)]", "POINT", "(2,0)"},
			{"?#", "lseg", "lseg", grS, "[(2,0),(2,2)]", "BOOL", true},
			{"#", "line", "line", grL, grL, "POINT", nil},
			{"?#", "line", "line", grL, grL, "BOOL", false},
			{"<->", "line", "line", grL, grL, "FLOAT8", float64(0)},
			{"#", "line", "line", grL, "{0,100,0}", "POINT", nil},
			{"<->", "line", "line", grL, "{0,2,-6}", "FLOAT8", float64(3)},
			{"<->", "box", "box", grB, "(3,2),(1,0)", "FLOAT8", float64(1)},
			{"?#", "lseg", "box", "[(0.5,0.5),(1.5,1.5)]", grB, "BOOL", true},
		})
	})
}
