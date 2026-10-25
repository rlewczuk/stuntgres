package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasurePathValues(t *testing.T) {
	geometryMeasurePathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range geometryMeasurePathCases {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, name := range []string{tc.function, "pg_catalog." + tc.function, "\"" + tc.function + "\""} {
					for _, operand := range []string{tc.formal + " '" + tc.input + "'", "NULL::" + tc.formal} {
						want := tc.want
						if operand == "NULL::"+tc.formal {
							want = nil
						}
						s := geometryMeasurePathSelect(b, "SELECT "+name+"("+operand+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{want}})
						run(s)
						run(fixture.Prepared(s))
					}
				}
			})
		}
		t.Run("scalar-variadic", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			s := geometryMeasurePathSelect(b, "SELECT pclose(VARIADIC path '[(0,0)]') AS v", []string{"v"}, []string{"PATH"}, [][]any{{"((0,0))"}})
			run(s)
			run(fixture.Prepared(s))
		})
		for _, tc := range []struct {
			input                string
			open                 bool
			count                int64
			length, closedLength float64
			area                 any
		}{
			{"[(0,0),(4,0),(0,3)]", true, 3, 9, 12, nil},
			{"((0,0),(4,0),(0,3))", false, 3, 12, 12, float64(6)},
			{"[(-1,0),(1,0)]", true, 2, 2, 4, nil},
			{"((-1,0),(1,0))", false, 2, 4, 4, float64(0)},
			{"[(2,3)]", true, 1, 0, 0, nil},
			{"((2,3))", false, 1, 0, 0, float64(0)},
			{"[(0,0),(0,0),(3,4),(0,0)]", true, 4, 10, 10, nil},
			{"((0,0),(0,0),(3,4),(0,0))", false, 4, 10, 10, float64(0)},
		} {
			t.Run(tc.input, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "SELECT isopen(p) AS o,isclosed(p) AS c,npoints(p) AS n,length(p) AS l,length(pclose(p)) AS cl,area(p) AS a,isopen(p) AS original FROM (SELECT $1::path AS p) AS source"
				s := geometryMeasurePathSelect(b, q, []string{"o", "c", "n", "l", "cl", "a", "original"}, []string{"BOOL", "BOOL", "INT4", "FLOAT8", "FLOAT8", "FLOAT8", "BOOL"}, [][]any{{tc.open, !tc.open, tc.count, tc.length, tc.closedLength, tc.area, tc.open}}, tc.input)
				run(s)
				run(fixture.Prepared(s))
			})
		}
		q := "SELECT length(p) AS a,length(pclose(p)) AS b,length(popen(pclose(p))) AS c,area(p) AS d,area(pclose(p)) AS e,area(popen(pclose(p))) AS f,popen(popen(p)) AS g,pclose(pclose(p)) AS h FROM (SELECT path '[(0,0),(4,0),(0,3)]' AS p) AS source"
		s := geometryMeasurePathSelect(b, q, []string{"a", "b", "c", "d", "e", "f", "g", "h"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "PATH", "PATH"}, [][]any{{float64(9), float64(12), float64(9), nil, float64(6), nil, "[(0,0),(4,0),(0,3)]", "((0,0),(4,0),(0,3))"}})
		run(s)
		run(fixture.Prepared(s))
		// SQL boolean witnesses avoid NaN equality while retaining independent results.
		for _, expression := range []string{
			"length(lseg '[(3,4),(0,0)]')=5",
			"length(lseg '[(0,0),(0,0)]')=0",
			"length(lseg '[(0,0),(5e-324,0)]')='5e-324'::float8",
			"length(lseg '[(0,0),(Infinity,NaN)]')='Infinity'::float8",
			"length(lseg '[(NaN,0),(1,0)]')='NaN'::float8",
			"length(path '[(NaN,Infinity)]')=0",
			"length(path '((NaN,Infinity))')='NaN'::float8",
			"isopen(popen(pclose(path '[(-0,5e-324),(-0,5e-324)]')))",
			"npoints(popen(pclose(path '[(-0,5e-324),(-0,5e-324)]')))=2",
		} {
			t.Run(expression, func(t *testing.T) {
				s := geometryMeasurePathSelect(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		// BIT and VARBIT have existing table/input support, but are not on the
		// explicit-cast annotation surface. Exercise their length overloads
		// without expanding that adjacent capability.
		run(fixture.Exec("CREATE TABLE geom_measure_path_length_inputs(f bit(3),v varbit)", 0))
		run(fixture.Exec("INSERT INTO geom_measure_path_length_inputs VALUES(B'101',B'101')", 1))
		for _, tc := range []struct {
			name, expression, source string
			want                     any
		}{
			{"text", "length('abc'::text)", "", int64(3)},
			{"bytea", "length('abc'::bytea)", "", int64(3)},
			{"bit", "length(f)", " FROM geom_measure_path_length_inputs", int64(3)},
			{"varbit", "length(v)", " FROM geom_measure_path_length_inputs", int64(3)},
			{"unknown-text", "length('abc')", "", int64(3)},
			{"unknown-null", "length(NULL)", "", nil},
		} {
			t.Run("preserve-length-"+tc.name, func(t *testing.T) {
				s := geometryMeasurePathSelect(b, "SELECT "+tc.expression+" AS v"+tc.source, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
	})
}
