package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently specified Table 9.36 witnesses. These expectations precede
// oracle discovery; neither operator admission nor expected values use native data.
type geometryRelationCase struct {
	op, left, right, a, b, typ string
	want                       any
}

const (
	grP = "(0,0)"
	grQ = "(4,0)"
	grR = "(1,1)"
	grB = "(2,2),(0,0)"
	grI = "(1.5,1.5),(0.5,0.5)"
	grS = "[(0,0),(2,0)]"
	grT = "[(1,-1),(1,1)]"
	grU = "[(0,3),(2,3)]"
	grV = "[(0,1),(2,1)]"
	grL = "{0,1,0}"
	grM = "{1,0,-1}"
	grH = "[(0,0),(2,0)]"
	grK = "[(1,-1),(1,1)]"
	grA = "((0,0),(2,0),(2,2),(0,2))"
	grE = "((0.5,0.5),(1.5,0.5),(1.5,1.5),(0.5,1.5))"
	grD = "((4,0),(6,0),(6,2),(4,2))"
	grO = "<(0,0),1>"
	grN = "<(4,0),1>"
	grG = "<(0,0),3>"
)

var geometryRelationCases = []geometryRelationCase{
	{"#", "lseg", "lseg", grS, grT, "POINT", "(1,0)"},
	{"#", "line", "line", grL, grM, "POINT", "(1,0)"},
	{"#", "box", "box", grB, grI, "BOX", grI},
	{"##", "point", "box", grQ, grB, "POINT", "(2,0)"},
	{"##", "point", "lseg", grR, grS, "POINT", "(1,0)"},
	{"##", "point", "line", grR, grL, "POINT", "(1,0)"},
	{"##", "lseg", "box", grV, grB, "POINT", "(1,1)"},
	{"##", "lseg", "lseg", grS, grT, "POINT", "(1,0)"},
	{"##", "line", "lseg", grL, grT, "POINT", "(1,0)"},
	{"<->", "point", "point", grP, "(3,4)", "FLOAT8", float64(5)},
	{"<->", "box", "box", grB, "(6,2),(4,0)", "FLOAT8", float64(4)},
	{"<->", "lseg", "lseg", grS, grU, "FLOAT8", float64(3)},
	{"<->", "line", "line", grL, grM, "FLOAT8", float64(0)},
	{"<->", "path", "path", grH, "[(0,3),(2,3)]", "FLOAT8", float64(3)},
	{"<->", "polygon", "polygon", grA, grD, "FLOAT8", float64(2)},
	{"<->", "circle", "circle", grO, grN, "FLOAT8", float64(2)},
	{"<->", "point", "box", grQ, grB, "FLOAT8", float64(2)},
	{"<->", "box", "point", grB, grQ, "FLOAT8", float64(2)},
	{"<->", "point", "lseg", grR, grS, "FLOAT8", float64(1)},
	{"<->", "lseg", "point", grS, grR, "FLOAT8", float64(1)},
	{"<->", "point", "line", grR, grL, "FLOAT8", float64(1)},
	{"<->", "line", "point", grL, grR, "FLOAT8", float64(1)},
	{"<->", "point", "path", grR, grH, "FLOAT8", float64(1)},
	{"<->", "path", "point", grH, grR, "FLOAT8", float64(1)},
	{"<->", "point", "polygon", grQ, grA, "FLOAT8", float64(2)},
	{"<->", "polygon", "point", grA, grQ, "FLOAT8", float64(2)},
	{"<->", "point", "circle", grQ, grO, "FLOAT8", float64(3)},
	{"<->", "circle", "point", grO, grQ, "FLOAT8", float64(3)},
	{"<->", "box", "lseg", grB, grU, "FLOAT8", float64(1)},
	{"<->", "lseg", "box", grU, grB, "FLOAT8", float64(1)},
	{"<->", "lseg", "line", grU, grL, "FLOAT8", float64(3)},
	{"<->", "line", "lseg", grL, grU, "FLOAT8", float64(3)},
	{"<->", "polygon", "circle", grA, grN, "FLOAT8", float64(1)},
	{"<->", "circle", "polygon", grN, grA, "FLOAT8", float64(1)},
	{"@>", "box", "point", grB, grR, "BOOL", true},
	{"@>", "box", "box", grB, grI, "BOOL", true},
	{"@>", "path", "point", grH, grP, "BOOL", true},
	{"@>", "polygon", "point", grA, grR, "BOOL", true},
	{"@>", "polygon", "polygon", grA, grE, "BOOL", true},
	{"@>", "circle", "point", grO, grP, "BOOL", true},
	{"@>", "circle", "circle", grG, grO, "BOOL", true},
	{"<@", "point", "box", grR, grB, "BOOL", true},
	{"<@", "point", "lseg", grP, grS, "BOOL", true},
	{"<@", "point", "line", grP, grL, "BOOL", true},
	{"<@", "point", "path", grP, grH, "BOOL", true},
	{"<@", "point", "polygon", grR, grA, "BOOL", true},
	{"<@", "point", "circle", grP, grO, "BOOL", true},
	{"<@", "box", "box", grI, grB, "BOOL", true},
	{"<@", "lseg", "box", grV, grB, "BOOL", true},
	{"<@", "lseg", "line", grS, grL, "BOOL", true},
	{"<@", "polygon", "polygon", grE, grA, "BOOL", true},
	{"<@", "circle", "circle", grO, grG, "BOOL", true},
	{"&&", "box", "box", grB, grI, "BOOL", true},
	{"&&", "polygon", "polygon", grA, grE, "BOOL", true},
	{"&&", "circle", "circle", grO, grG, "BOOL", true},
	{"?#", "box", "box", grB, grI, "BOOL", true},
	{"?#", "lseg", "box", grV, grB, "BOOL", true},
	{"?#", "lseg", "lseg", grS, grT, "BOOL", true},
	{"?#", "lseg", "line", grT, grL, "BOOL", true},
	{"?#", "line", "box", grL, grB, "BOOL", true},
	{"?#", "line", "line", grL, grM, "BOOL", true},
	{"?#", "path", "path", grH, grK, "BOOL", true},
}

func geometryRelationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func geometryRelationExpr(tc geometryRelationCase) string {
	return tc.left + " " + boxQuote(tc.a) + " " + tc.op + " " + tc.right + " " + boxQuote(tc.b)
}

// All routes inspect actual returned metadata (including empty and NULL cursors)
// using the existing independent raw geometry adapter contract.
func geometryRelationWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tc geometryRelationCase) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	literal := "SELECT " + geometryRelationExpr(tc) + " AS v"
	s := boxSelect(b, literal, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
	run(s)
	run(fixture.Prepared(s))
	run(boxSelect(b, literal+" WHERE FALSE", []string{"v"}, []string{tc.typ}, nil))
	q := "SELECT $1::" + tc.left + " " + tc.op + " $2::" + tc.right + " AS v"
	s = boxSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.a, tc.b)
	run(s)
	run(fixture.Prepared(s))
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, args := range [][]any{{tc.a, []byte(tc.b)}, {[]byte(tc.a), tc.b}, {nil, tc.b}, {tc.a, nil}, {nil, nil}, {[]byte(nil), []byte(tc.b)}, {[]byte(tc.a), []byte(nil)}, {tc.a, tc.b}} {
		want := tc.want
		if args[0] == nil || args[1] == nil {
			want = nil
		}
		for _, arg := range args {
			if v, ok := arg.([]byte); ok && v == nil {
				want = nil
			}
		}
		s := boxSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
		run(s)
		run(fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for mask := 1; mask < 4; mask++ {
		a, z := tc.left+" "+boxQuote(tc.a), tc.right+" "+boxQuote(tc.b)
		if mask&1 != 0 {
			a = "NULL::" + tc.left
		}
		if mask&2 != 0 {
			z = "NULL::" + tc.right
		}
		null := boxSelect(b, "SELECT "+a+" "+tc.op+" "+z+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{nil}})
		run(null)
		run(fixture.Prepared(null))
	}
}

func TestReferenceGeometryRelationMatrix(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, tc := range geometryRelationCases {
			t.Run(fmt.Sprintf("%02d_%s_%s_%s", i+1, tc.left, tc.op, tc.right), func(t *testing.T) { geometryRelationWitness(t, p, b, c, tc) })
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE geom_relation_matrix(p point,s lseg,a box,b box,l line,m line)", 0))
		run(fixture.Exec("INSERT INTO geom_relation_matrix VALUES('(1,1)','[(0,0),(2,0)]','(2,2),(0,0)','(1.5,1.5),(0.5,0.5)','{0,1,0}','{1,0,-1}')", 1))
		for _, q := range []string{
			"SELECT p ## s AS closest,a # b AS common,p <-> s AS distance,l ?# m AS hit FROM geom_relation_matrix",
			"SELECT '[(0,0),(2,0)]' OPERATOR(pg_catalog.##) lseg '[(1,-1),(1,1)]' AS closest,box '(2,2),(0,0)' # '(1.5,1.5),(0.5,0.5)' AS common,point '(1,1)' <-> '(1,0)' AS distance,line '{0,1,0}' ?# '{1,0,-1}' AS hit",
		} {
			s := boxSelect(b, q, []string{"closest", "common", "distance", "hit"}, []string{"POINT", "BOX", "FLOAT8", "BOOL"}, [][]any{{"(1,0)", grI, float64(1), true}})
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
