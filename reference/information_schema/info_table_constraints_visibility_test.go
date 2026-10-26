package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func tcAccessDenied(run func(fixture.Step),schema bool) {
 message:="permission denied for table vis"
 query:="SELECT * FROM tc_private.vis"
 d:=fixture.ControlsReferenceDiagnostic("42501",message)
 if schema { d=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema tc_private");d.Position="15" }
 for _,prepared:=range []bool{false,true} {
  s:=fixture.ErrorDiagnostic(query,d);s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute"
  if schema&&prepared { s.ErrorStage="prepare" };run(s)
 }
}

func TestReferenceInfoTableConstraintsVisibility(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  exec:=func(queries ...string) { for _,q:=range queries { run(fixture.Exec(q,0)) } }
  exec("CREATE SCHEMA tc_scope","CREATE ROLE tc_owner","CREATE ROLE tc_reader","CREATE ROLE tc_member INHERIT","CREATE ROLE tc_set NOINHERIT","CREATE ROLE tc_noinherit NOINHERIT","CREATE ROLE tc_group INHERIT",
   "GRANT tc_owner TO tc_member","GRANT tc_owner TO tc_set WITH INHERIT FALSE,SET TRUE","GRANT tc_owner TO tc_noinherit","GRANT tc_reader TO tc_group",
   "CREATE TABLE tc_scope.vis(a integer,b integer,c integer,d integer,CONSTRAINT vis_check CHECK(a>0),CONSTRAINT vis_unique UNIQUE(b))",
   "ALTER TABLE tc_scope.vis OWNER TO tc_owner","REVOKE ALL ON tc_scope.vis FROM PUBLIC,tc_owner")
  where:="table_schema IN ('tc_scope','tc_private') AND table_name='vis'"
  held:=dcHold(t,p,c,tcProjection(b.PG,where,nil).Query)
  want:=[][]any{tcRow(database,"tc_scope","vis","vis_check","CHECK",nil),tcRow(database,"tc_scope","vis","vis_unique","UNIQUE","YES")}
  check:=func(rows [][]any) { tcRoutes(t,p,c,r,held,tcProjection(b.PG,where,rows)) }
  check(want)
  for _,role:=range []string{"tc_owner","tc_member","tc_reader","tc_set","tc_noinherit"} {
   exec("SET ROLE "+role)
   if role=="tc_owner"||role=="tc_member" { check(want) } else { check(nil) }
   exec("RESET ROLE")
  }
  exec("SET ROLE tc_set","SET ROLE tc_owner");check(want);exec("RESET ROLE")
  for _,right:=range []string{"SELECT","MAINTAIN","INSERT","UPDATE","DELETE","TRUNCATE","REFERENCES","TRIGGER"} {
   exec("GRANT "+right+" ON tc_scope.vis TO tc_reader","SET ROLE tc_reader")
   if right=="SELECT"||right=="MAINTAIN" { check(nil) } else { check(want) }
   exec("RESET ROLE","REVOKE "+right+" ON tc_scope.vis FROM tc_reader","SET ROLE tc_reader");check(nil);exec("RESET ROLE")
  }
  for _,right:=range []string{"SELECT","INSERT","UPDATE","REFERENCES"} {
   exec("GRANT "+right+"(c) ON tc_scope.vis TO tc_reader","SET ROLE tc_reader")
   if right=="SELECT" { check(nil) } else { check(want) }
   exec("RESET ROLE","REVOKE "+right+"(c) ON tc_scope.vis FROM tc_reader","SET ROLE tc_reader");check(nil);exec("RESET ROLE")
  }
  exec("GRANT INSERT ON tc_scope.vis TO PUBLIC","SET ROLE tc_reader");check(want)
  exec("RESET ROLE","REVOKE INSERT ON tc_scope.vis FROM PUBLIC","GRANT UPDATE(c) ON tc_scope.vis TO tc_reader","SET ROLE tc_group");check(want)
  exec("RESET ROLE","REVOKE UPDATE(c) ON tc_scope.vis FROM tc_reader","SET ROLE tc_group");check(nil)
  exec("RESET ROLE","GRANT INSERT(d) ON tc_scope.vis TO tc_reader","ALTER TABLE tc_scope.vis DROP COLUMN d","SET ROLE tc_reader");check(nil)
  exec("RESET ROLE","ALTER TABLE tc_scope.vis OWNER TO tc_reader","REVOKE ALL ON tc_scope.vis FROM tc_reader","SET ROLE tc_reader");check(want)
  exec("RESET ROLE","ALTER TABLE tc_scope.vis OWNER TO tc_owner","CREATE SCHEMA tc_private","ALTER TABLE tc_scope.vis SET SCHEMA tc_private","REVOKE ALL ON SCHEMA tc_private FROM PUBLIC,tc_reader","GRANT INSERT ON tc_private.vis TO tc_reader","SET ROLE tc_reader")
  want=[][]any{tcRow(database,"tc_private","vis","vis_check","CHECK",nil),tcRow(database,"tc_private","vis","vis_unique","UNIQUE","YES")}
  check(want);tcAccessDenied(run,true);check(want)
  exec("RESET ROLE","GRANT USAGE ON SCHEMA tc_private TO tc_reader","SET ROLE tc_reader")
  check(want);tcAccessDenied(run,false)
  exec("RESET ROLE")
  run(fixture.Exec("INSERT INTO tc_private.vis VALUES(1,1,1)",1))
  exec("GRANT SELECT ON tc_private.vis TO tc_reader","ALTER TABLE tc_private.vis ENABLE ROW LEVEL SECURITY","SET ROLE tc_reader")
  check(want)
  run(fixture.Select("SELECT * FROM tc_private.vis",[]string{"a","b","c"},[]string{"INT4","INT4","INT4"},nil))
  exec("RESET ROLE","REVOKE INSERT ON tc_private.vis FROM tc_reader","SET ROLE tc_reader");check(nil)
  exec("RESET ROLE")
 })
}

func TestReferenceInfoTableConstraintsFeatureSmoke(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  tcSeed(run)
  s:=tcProjection(b.PG,"table_schema='tc_scope'",tcSeedRows(database))
  tcRoutes(t,p,c,r,dcHold(t,p,c,s.Query),s)
  tcIdentity(t,p,b,c,r)
  for _,q:=range []string{"CREATE ROLE tc_reader","REVOKE ALL ON SCHEMA tc_scope FROM PUBLIC","GRANT SELECT ON tc_scope.a TO tc_reader","SET ROLE tc_reader"} { run(fixture.Exec(q,0)) }
  reader:=dcHold(t,p,c,tcProjection(b.PG,"table_schema='tc_scope' AND table_name='a'",nil).Query)
  tcRoutes(t,p,c,r,reader,tcProjection(b.PG,"table_schema='tc_scope' AND table_name='a'",nil))
  run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("GRANT INSERT(payload) ON tc_scope.a TO tc_reader",0));run(fixture.Exec("SET ROLE tc_reader",0))
  var aRows [][]any
  for _,row:=range tcSeedRows(database) { if row[5]=="a" { aRows=append(aRows,row) } }
  tcRoutes(t,p,c,r,reader,tcProjection(b.PG,"table_schema='tc_scope' AND table_name='a'",aRows))
  d:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema tc_scope");d.Position="15"
  for _,prepared:=range []bool{false,true} {
   step:=fixture.ErrorDiagnostic("SELECT * FROM tc_scope.a",d);step.SelectRows,step.Prepared,step.ErrorStage=true,prepared,"execute"
   if prepared { step.ErrorStage="prepare" };run(step)
  }
  tcMissing(run)
  tcRoutes(t,p,c,r,reader,tcProjection(b.PG,"table_schema='tc_scope' AND table_name='a'",aRows))
  run(fixture.Exec("RESET ROLE",0))
 })
}
