package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceFTSStatReadOnlyAdmission(t *testing.T) {
 ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  c:=ftsInspectionConnection(t,p,b)
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE TABLE fts_stat_readonly(ord integer,v tsvector)",0))
  run(fixture.Exec("INSERT INTO fts_stat_readonly VALUES(1,'cat:1')",1))
  run(fixture.Exec("CREATE VIEW fts_stat_readonly_locked AS SELECT v FROM fts_stat_readonly FOR UPDATE NOWAIT",0))
  holder,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer holder.Close()
  fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Exec("BEGIN",0));defer holder.ExecContext(p.Ctx,"ROLLBACK")
  fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT ord FROM fts_stat_readonly FOR UPDATE",[]string{"ord"},[]string{"INT4"},[][]any{{int64(1)}}))
  // An actually executed row mark conflicts. Read-only cursor admission must
  // instead reject before acquiring/waiting on any of these same tuple locks.
  lockFailure:=fixture.ControlsReferenceDiagnostic("55P03",`could not obtain lock on row in relation "fts_stat_readonly"`)
  ftsInspectionDiagnostic(t,p,b,c,"SELECT ord FROM fts_stat_readonly FOR UPDATE NOWAIT",lockFailure,false)
  const query="SELECT * FROM ts_stat($1::text) ORDER BY word"
  const plain="SELECT v FROM fts_stat_readonly"
  statement,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
  want:=[][]any{{"cat",int64(1),int64(1)}}
  for _,tc:=range []struct{name,inner string;reject bool}{
   {"derived","SELECT v FROM (SELECT v FROM fts_stat_readonly FOR UPDATE NOWAIT) AS locked",true},
   {"stored_view","SELECT v FROM fts_stat_readonly_locked",true},
   {"unused_cte","WITH unused AS (SELECT v FROM fts_stat_readonly FOR UPDATE NOWAIT) SELECT v FROM fts_stat_readonly",false},
  } {t.Run(tc.name,func(t *testing.T){
   for _,route:=range []string{"direct","fresh","held"}{for _,queryMode:=range []bool{false,true}{
    var executor fixture.Executor=c;if route=="held"{executor=held}
    var s fixture.Step
    if tc.reject {
     diagnostic:=fixture.ControlsReferenceDiagnostic("0A000","SELECT FOR UPDATE is not allowed in a non-volatile function")
     diagnostic.Context="SQL statement \""+tc.inner+"\""
     s=fixture.ErrorDiagnostic(query,diagnostic,tc.inner);s.SelectRows=queryMode;s.ErrorStage="execute"
    } else if queryMode {s=fixture.Select(query,ftsStatColumns,ftsStatTypes,want,tc.inner)} else {s=fixture.Exec(query,1,tc.inner)}
    s.Prepared=route=="fresh"
    fixture.RunStep(t,p.Ctx,executor,b.Recorder,s)
    // Reuse the same held statement after each actual failure, including the
    // Exec path. A plain scan remains valid despite the conflicting holder.
    var recovery fixture.Step
    if queryMode{recovery=fixture.Select(query,ftsStatColumns,ftsStatTypes,want,plain)}else{recovery=fixture.Exec(query,1,plain)}
    recovery.Prepared=route=="fresh"
    fixture.RunStep(t,p.Ctx,executor,b.Recorder,recovery)
   }}
   run(fixture.Select("SELECT ord,v FROM fts_stat_readonly",[]string{"ord","v"},[]string{"INT4","TSVECTOR"},[][]any{{int64(1),"'cat':1"}}))
  })}
  run(fixture.Select(query,ftsStatColumns,ftsStatTypes,want,plain))
 })
}
