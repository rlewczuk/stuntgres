package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeTransformDiagnostics(t *testing.T) {
	for _, tc := range []struct { name, source, indexes string; want fixture.Diagnostic }{
		{"null_element","1","{NULL}",cubeConstructorDiagnostic("2202E","cannot work with arrays containing NULLs","")},
		{"null_before_length_and_bounds","1","{0,"+strings.Repeat("1,",99)+"NULL}",cubeConstructorDiagnostic("2202E","cannot work with arrays containing NULLs","")},
		{"length_before_bounds","1","{0,"+strings.Repeat("1,",99)+"1}",cubeConstructorLimit("array is too long")},
		{"zero","1","{0}",cubeConstructorDiagnostic("2202E","Index out of bounds","")},
		{"negative","1","{-1}",cubeConstructorDiagnostic("2202E","Index out of bounds","")},
		{"int_min","1","{-2147483648}",cubeConstructorDiagnostic("2202E","Index out of bounds","")},
		{"int_max","1","{2147483647}",cubeConstructorDiagnostic("2202E","Index out of bounds","")},
		{"dimension","(1,2)","{3}",cubeConstructorDiagnostic("2202E","Index out of bounds","")},
		{"zero_dimension","()","{1}",cubeConstructorDiagnostic("2202E","Index out of bounds","")},
	} {
		t.Run(tc.name,func(t *testing.T) {
			cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				cubeInstall(t,p,b,c)
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				run(fixture.Exec("CREATE TABLE cube_transform_diag(id integer NOT NULL,v cube)",0))
				run(fixture.Exec("INSERT INTO cube_transform_diag VALUES(1,'(7)'::cube),(2,'(8)'::cube)",2))
				baseline := fixture.CubeSelect(b,"SELECT id,v FROM cube_transform_diag ORDER BY id",[]string{"id","v"},[][]any{{int64(1),"(7)"},{int64(2),"(8)"}})
				literal := "cube_subset("+cubeLiteral(tc.source)+"::cube,"+cubeLiteral(tc.indexes)+"::integer[])"
				bound := "cube_subset($1::cube,$2::integer[])"
				for _, prepared := range []bool{false,true} {
					for _, route := range []struct { q string; args []any; query bool }{
						{"/*é*/ SELECT "+literal+" AS v",nil,true},
						{"SELECT "+bound+" AS v",[]any{tc.source,tc.indexes},true},
						{"INSERT INTO cube_transform_diag VALUES(3,'(3)'::cube),(4,"+literal+")",nil,false},
						{"INSERT INTO cube_transform_diag VALUES(3,'(3)'::cube),(4,"+bound+")",[]any{tc.source,tc.indexes},false},
						{"UPDATE cube_transform_diag SET v=CASE WHEN id=1 THEN '(3)'::cube ELSE "+bound+" END",[]any{tc.source,tc.indexes},false},
					} {
						cubeError(t,p,b,c,route.q,prepared,route.query,false,tc.want,route.args...)
						run(baseline)
					}
				}
				q := "SELECT "+bound+" AS v"
				held := cubeConstructorHeld(t,p,c,q)
				defer held.Statement.Close()
				good := cubeTransformSelect(b,q,"(1)","1","{1}")
				fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
				bad := fixture.ErrorDiagnostic(q,tc.want,tc.source,tc.indexes)
				bad.SelectRows,bad.ErrorStage = true,"execute"
				fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
				fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
				run(baseline)
			})
		})
	}
	t.Run("producers",func(t *testing.T) {
		cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			cubeInstall(t,p,b,c)
			for _, tc := range []struct { literal,bound,input,position string; parameter int; want fixture.Diagnostic }{
				{"/*é*/ SELECT cube_subset('(1,)'::cube,'{1}'::integer[])","SELECT cube_subset($1::cube,'{1}'::integer[])","(1,)","26",1,cubeSyntaxDiagnostic(`syntax error at or near ")"`)},
				{"/*é*/ SELECT cube_subset('(1)'::cube,'{oops}'::integer[])","SELECT cube_subset('(1)'::cube,$1::integer[])","{oops}","38",1,cubeConstructorDiagnostic("22P02",`invalid input syntax for type integer: "oops"`,"")},
				{"/*é*/ SELECT cube_subset('(1)'::cube,'{1,,2}'::integer[])","SELECT cube_subset('(1)'::cube,$1::integer[])","{1,,2}","38",1,cubeConstructorDiagnostic("22P02",`malformed array literal: "{1,,2}"`,`Unexpected "," character.`)},
			} {
				for _, prepared := range []bool{false,true} {
					literal := tc.want
					literal.Position = tc.position
					cubeError(t,p,b,c,tc.literal,prepared,true,true,literal)
					cubeError(t,p,b,c,tc.bound,prepared,true,false,cubeParameterDiagnostic(tc.want,tc.input,tc.parameter),tc.input)
				}
			}
		})
	})
	t.Run("empty_source_analysis",func(t *testing.T) {
		cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			cubeInstall(t,p,b,c)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE cube_transform_empty(v cube)",0))
			want := fixture.ControlsReferenceDiagnostic("42703",`column "missing" does not exist`)
			want.Position = "25"
			for _, prepared := range []bool{false,true} {
				cubeError(t,p,b,c,"SELECT cube_subset(NULL,missing) FROM cube_transform_empty",prepared,true,true,want)
			}
		})
	})
}
