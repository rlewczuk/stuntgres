package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALPositionsDiagnostics(t *testing.T) {
	for _,name := range walPositionNames {
		t.Run(name,func(t *testing.T) {
			recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				type sample struct { query,code,message,hint string; position int }
				args := []struct{sql,typ string}{{"1","integer"},{"NULL","unknown"},{"NULL::text","text"},{"x=>1","x => integer"}}
				if name=="pg_create_restore_point" { args = []struct{sql,typ string}{{"",""},{"'a','b'","unknown, unknown"},{"1","integer"},{"true","boolean"},{"name=>'x'","name => unknown"},{"name:='x'","name => unknown"}} }
				var cases []sample
				for _,arg := range args { cases = append(cases,sample{"SELECT "+name+"("+arg.sql+")","42883","function "+name+"("+arg.typ+") does not exist",hint,8}) }
				valid := ""; if name=="pg_create_restore_point" { valid="'x'" }
				cases = append(cases,
					sample{"SELECT public."+name+"("+valid+")","42883","function public."+name+"("+func() string { if valid!="" { return "unknown" }; return "" }()+") does not exist",hint,8},
					sample{"SELECT missing_wal_schema."+name+"()","3F000",`schema "missing_wal_schema" does not exist`,"",8},
					sample{`SELECT "`+strings.ToUpper(name)+`"()`,"42883","function "+strings.ToUpper(name)+"() does not exist",hint,8},
					sample{"SELECT "+name+"(missing_wal_argument)","42703",`column "missing_wal_argument" does not exist`,"",9+len(name)},
					sample{"/*é🙂*/ SELECT "+name+"(1)","42883","function "+name+"(integer) does not exist",hint,15},
					sample{"SELECT "+name+"("+valid+") OVER ()","42809","OVER specified, but "+name+" is not a window function nor an aggregate function","",8},
					sample{"SELECT "+name+"("+valid+") FILTER (WHERE true)","42809","FILTER specified, but "+name+" is not an aggregate function","",8},
				)
				if name=="pg_create_restore_point" {
					cases=append(cases,sample{"SELECT "+name+"(*)","42883","function "+name+"() does not exist",hint,8})
				} else {
					cases=append(cases,sample{"SELECT "+name+"(*)","42809",name+"(*) specified, but "+name+" is not an aggregate function","",8})
				}
				if name=="pg_create_restore_point" {
					cases=append(cases,
						sample{"SELECT "+name+"(DISTINCT 'x')","42809","DISTINCT specified, but "+name+" is not an aggregate function","",8},
						sample{"SELECT "+name+"('x' ORDER BY 1)","42809","ORDER BY specified, but "+name+" is not an aggregate function","",8},
						sample{"SELECT "+name+"(VARIADIC ARRAY['x'])","42883","function "+name+"(text[]) does not exist",hint,8},
					)
				}
				for _,tc := range cases {
					for _,prepared := range []bool{false,true} {
						s := fixture.ImplicitError(tc.query,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:fixture.Position(tc.position)})
						s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"; if prepared { s.ErrorStage="prepare" }
						fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
					}
				}
			})
		})
	}
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		q := "SELECT pg_current_wal_lsn_typo()"
		for _,prepared := range []bool{false,true} {
			s := fixture.ImplicitError(q,fixture.Diagnostic{Code:"42883",Message:"function pg_current_wal_lsn_typo() does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)})
			s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute"; if prepared { s.ErrorStage="prepare" }; fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		}
	})
}

func TestReferenceWALPositionsRestoreNames(t *testing.T) {
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE wal_positions_names(v text)",0))
		for i,name := range []string{"",strings.Repeat("a",63),strings.Repeat("a",64),strings.Repeat("é",31)+"a",strings.Repeat("é",32),strings.Repeat("é",31)+"ab","  quoted ' marker  "} {
			affected := int64(1); if i==0 { affected=0 }
			run(fixture.Exec("DELETE FROM wal_positions_names",affected))
			run(fixture.Exec("INSERT INTO wal_positions_names VALUES($1)",1,name))
			literal := "'"+strings.ReplaceAll(name,"'","''")+"'"
			for _,route := range []struct{q string; args []any}{{"SELECT pg_create_restore_point("+literal+")",nil},{"SELECT pg_create_restore_point($1::text)",[]any{name}},{"SELECT pg_create_restore_point(v) FROM wal_positions_names",nil}} {
				if len(name)<64 { walPositionBoundaryRoutes(t,p,b,c,route.q,"pg_create_restore_point",route.args...) } else {
					s := replicationMessageError(route.q,"22023","value too long for restore point (maximum 63 characters)",""); s.Args=route.args; replicationMessageRoutes(t,p,b,c,s)
				}
			}
		}
		q := "SELECT pg_create_restore_point((1/$1::integer)::text)"
		s := replicationMessageError(q,"22012","division by zero",""); s.Args=[]any{0}; replicationMessageRoutes(t,p,b,c,s)
	})
}
