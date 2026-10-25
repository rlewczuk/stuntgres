package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

type cubeTransformCase struct {
	name, literal, bound string
	args []any
	want any
}

var cubeTransformCases = []cubeTransformCase{
	{"subset", "cube_subset('(1,3,5),(6,7,8)'::cube,'{3,2,1,1}'::integer[])", "cube_subset($1::cube,$2::integer[])", []any{"(1,3,5),(6,7,8)","{3,2,1,1}"}, "(5, 3, 1, 1),(8, 7, 6, 6)"},
	{"union", "cube_union('(1,2)'::cube,'(3,4)'::cube)", "cube_union($1::cube,$2::cube)", []any{"(1,2)","(3,4)"}, "(1, 2),(3, 4)"},
	{"inter", "cube_inter('(1,2)'::cube,'(3,4)'::cube)", "cube_inter($1::cube,$2::cube)", []any{"(1,2)","(3,4)"}, "(3, 4),(1, 2)"},
	{"enlarge", "cube_enlarge('(1,2),(3,4)'::cube,0.5,3)", "cube_enlarge($1::cube,$2::float8,$3::integer)", []any{"(1,2),(3,4)",.5,3}, "(0.5, 1.5, -0.5),(3.5, 4.5, 0.5)"},
}

func cubeTransformSelect(b fixture.Backend, q string, want any, args ...any) fixture.Step {
	s := cubeConstructorSelect(b,q,[]string{"v"},[]string{"CUBE"},[][]any{{want}},args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t,b.PG,rows,[]string{"v"},nil) }
	return s
}

func TestReferenceCubeTransformValues(t *testing.T) {
	for _, tc := range cubeTransformCases {
		t.Run(tc.name,func(t *testing.T) {
			cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				run(fixture.Exec("CREATE EXTENSION cube",0))
				literal := cubeTransformSelect(b,"SELECT "+tc.literal+" AS v",tc.want)
				run(literal)
				run(fixture.Prepared(literal))
				q := "SELECT "+tc.bound+" AS v"
				held := cubeConstructorHeld(t,p,c,q)
				defer held.Statement.Close()
				alternate := map[string]struct{args []any;want any}{
					"subset":{[]any{"(9,8,7)","{1,3}"},"(9, 7)"},
					"union":{[]any{"(4)","(2)"},"(2),(4)"},
					"inter":{[]any{"(1),(4)","(2),(3)"},"(2),(3)"},
					"enlarge":{[]any{"(2),(-2)",0,4},"(-2),(2)"},
				}[tc.name]
				for _, sample := range []struct{args []any;want any}{{tc.args,tc.want},alternate} {
					s := cubeTransformSelect(b,q,sample.want,sample.args...)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				}
				if tc.name == "subset" {
					for _, sample := range []struct{indexes,text string}{
						{"{1}","(1)"},{"{1,1}","(1, 1)"},{"{}","()"},
					} {
						s := cubeTransformSelect(b,q,sample.text,"(1,2),(1,3)",sample.indexes)
						run(s); run(fixture.Prepared(s))
						fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					}
				}
				for i := range tc.args {
					args := append([]any(nil),tc.args...)
					args[i] = nil
					s := cubeTransformSelect(b,q,nil,args...)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				}
			})
		})
	}
	for _, tc := range []struct { name, expression string; want any }{
		{"empty_subset","cube_subset('(1),(2)'::cube,'{}'::integer[])","()"},
		{"empty_box_geometry","cube_is_point(cube_subset('(1),(2)'::cube,'{}'::integer[]))",true},
		{"coincident_box_geometry","cube_is_point(cube_subset('(1,2),(1,3)'::cube,'{1}'::integer[]))",true},
		{"coincident_box_output","cube_subset('(1,2),(1,3)'::cube,'{1}'::integer[])","(1)"},
		{"repeated_coincident_box","cube_subset('(1,2),(1,3)'::cube,'{1,1}'::integer[])","(1, 1)"},
		{"nan_subset_box","cube_subset('(NaN,1),(NaN,2)'::cube,'{1}'::integer[])","(NaN),(NaN)"},
		{"nan_subset_geometry","cube_is_point(cube_subset('(NaN,1),(NaN,2)'::cube,'{1}'::integer[]))",false},
		{"empty_point_flag","cube_is_point(cube_subset('(1)'::cube,'{}'::integer[]))",true},
		{"flat","cube_subset('(1,3,5),(6,7,8)'::cube,'{{3,2},{1,1}}'::integer[])","(5, 3, 1, 1),(8, 7, 6, 6)"},
		{"lower_bounds","cube_subset('(1,3,5)'::cube,'[0:1]={2,1}'::integer[])","(3, 1)"},
		{"reversed","cube_subset('(6,7,8),(1,3,5)'::cube,'{3,2,1,1}'::integer[])","(8, 7, 6, 6),(5, 3, 1, 1)"},
		{"100_repeats","cube_subset('(1)'::cube,'"+cubeConstructorArray(100,"1")+"'::integer[])",cubeConstructorPoint(100,"1")},
		{"mismatch_union","cube_union('(1,2),(3,4)'::cube,'(5,6,7),(8,9,10)'::cube)","(1, 2, 0),(8, 9, 10)"},
		{"mismatch_reverse","cube_union('(5,6,7),(8,9,10)'::cube,'(1,2),(3,4)'::cube)","(1, 2, 0),(8, 9, 10)"},
		{"union_zero","cube_union('(0,5,2),(2,3,1)'::cube,'0'::cube)","(0, 0, 0),(2, 5, 2)"},
		{"inter","cube_inter('(1,2),(10,11)'::cube,'(3,14),(16,15)'::cube)","(3, 14),(10, 11)"},
		{"touch","cube_inter('(1,2),(10,11)'::cube,'(10,11),(12,13)'::cube)","(10, 11)"},
		{"touch_flag","cube_is_point(cube_inter('(1,2),(10,11)'::cube,'(10,11),(12,13)'::cube))",true},
		{"inter_padding","cube_inter('(0,-1),(1,1)'::cube,'(-2),(2)'::cube)","(0, 0),(1, 0)"},
		{"inter_padding_reverse","cube_inter('(-2),(2)'::cube,'(0,-1),(1,1)'::cube)","(0, 0),(1, 0)"},
		{"empty_union","cube_union('()'::cube,'()'::cube)","()"},
		{"empty_inter","cube_inter('()'::cube,'()'::cube)","()"},
		{"shrink","cube_enlarge('(2,-2),(-3,7)'::cube,-3,2)","(-0.5, 1),(-0.5, 4)"},
		{"collapse","cube_enlarge('(1,2),(3,4)'::cube,-2,0)","(2, 3)"},
		{"normalize","cube_enlarge('(2),(-2)'::cube,0,4)","(-2),(2)"},
		{"negative_n","cube_enlarge('(1),(2)'::cube,1,-1)","(0),(3)"},
		{"smaller_n","cube_enlarge('(1,2),(3,4)'::cube,1,1)","(0, 1),(4, 5)"},
		{"null_before_body","cube_subset(NULL::cube,'{NULL}'::integer[])",nil},
		{"empty_grow","cube_enlarge('()'::cube,1,2)","(-1, -1),(1, 1)"},
		{"precision","cube_subset('(0.10000000000000002)'::cube,'{1}'::integer[])","(0.10000000000000002)"},
		{"nan_radius","cube_enlarge('(1),(2)'::cube,'NaN'::float8,3)","(NaN),(NaN)"},
		{"infinite_radius","cube_enlarge('(1),(2)'::cube,'Infinity'::float8,2)","(-Infinity, -Infinity),(Infinity, Infinity)"},
		{"negative_infinite_radius","cube_enlarge('(1),(2)'::cube,'-Infinity'::float8,2)","(NaN),(NaN)"},
		{"ordered_nan_left","cube_union('NaN'::cube,'1'::cube)","(1)"},
		{"ordered_nan_right","cube_union('1'::cube,'NaN'::cube)","(NaN),(NaN)"},
		{"ordered_nan_inter_left","cube_inter('NaN'::cube,'1'::cube)","(1)"},
		{"ordered_nan_inter_right","cube_inter('1'::cube,'NaN'::cube)","(NaN),(NaN)"},
		{"nan_entered_lower","cube_enlarge('(NaN),(1)'::cube,0,1)","(NaN),(1)"},
		{"direct_average_overflow","cube_enlarge('(1.7e308)'::cube,-1e308,1)","(Infinity)"},
	} {
		t.Run(tc.name,func(t *testing.T) {
			cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				run(fixture.Exec("CREATE EXTENSION cube",0))
				typ := "CUBE"
				if _,ok := tc.want.(bool); ok { typ = "BOOL" }
				s := cubeConstructorSelect(b,"SELECT "+tc.expression+" AS v",[]string{"v"},[]string{typ},[][]any{{tc.want}})
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
	cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE EXTENSION cube",0))
		for _, n := range []int{101,2147483647} {
			for _, r := range []int{1,0,-1} {
				want := int64(1)
				if r > 0 { want = 100 }
				s := fixture.Select("SELECT cube_dim(cube_enlarge($1::cube,$2::float8,$3::integer)) AS n",[]string{"n"},[]string{"INT4"},[][]any{{want}},"1",r,n)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		for _, expression := range []string{"cube_subset('(-0)'::cube,'{1}'::integer[])","cube_subset('(-0,1),(0,2)'::cube,'{1}'::integer[])","cube_enlarge('(-0)'::cube,0,1)"} {
			// Binary float text is asserted separately from cube formatting.
			q := "SELECT cube_coord("+expression+",1) AS v"
			s := cubeCoordinateScalar(b,q,math.Copysign(0,-1))
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
