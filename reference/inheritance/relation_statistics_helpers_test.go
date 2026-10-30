package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

type relationStatsScenario struct{
 t *testing.T
 p *fixture.Pair
 b fixture.Backend
 conn *sql.Conn
 db *sql.DB
 notices *[]fixture.Diagnostic
 prepared bool
}
func withRelationStats(t *testing.T,body func(relationStatsScenario)){
 t.Helper()
 for _,prepared:=range []bool{false,true}{t.Run(fmt.Sprint(prepared),func(t *testing.T){p:=fixture.NewScenario(t);for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){var notices []fixture.Diagnostic;db,conn,recorder:=fixture.NoticeReferencePool(t,p,b.PG,&notices);b.Recorder=recorder;body(relationStatsScenario{t:t,p:p,b:b,conn:conn,db:db,notices:&notices,prepared:prepared})})}})}
}
func(s relationStatsScenario) run(step fixture.Step){
 s.t.Helper()
 before:=len(*s.notices)
 defer func(){if s.t.Failed()&&len(*s.notices)>before{s.t.Logf("relation statistics notices from failed step: %#v",(*s.notices)[before:])}}()
 if s.prepared{step=fixture.Prepared(step)}
 fixture.RunStep(s.t,s.p.Ctx,s.conn,s.b.Recorder,step)
}
func(s relationStatsScenario) exec(q string){s.run(fixture.Exec(q,0))}
func relationStatsRestore(name,extra string)string{if extra!=""{extra=","+extra};return fmt.Sprintf("SELECT pg_restore_relation_stats('schemaname','public','relname','%s'%s)",name,extra)}
func(s relationStatsScenario) restore(extra string,want bool){s.t.Helper();s.run(fixture.Select(relationStatsRestore("rst_t",extra),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{want}}))}
func relationStatsRead(name string)string{return fmt.Sprintf("SELECT relpages,reltuples,relallvisible,relallfrozen FROM pg_class WHERE oid='public.%s'::regclass",name)}
func(s relationStatsScenario) stats(pages int64,tuples float64,visible,frozen int64){s.t.Helper();s.run(fixture.Select(relationStatsRead("rst_t"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{pages,tuples,visible,frozen}}))}
func(s relationStatsScenario) clear(){s.run(fixture.Select("SELECT pg_clear_relation_stats('public','rst_t')",[]string{"pg_clear_relation_stats"},[]string{"VOID"},[][]any{{""}}))}
func(s relationStatsScenario) fail(q,code,message string){s.t.Helper();s.run(fixture.ImplicitError(q,fixture.ControlsReferenceDiagnostic(code,message)))}
func relationStatsWarning(code,message string)fixture.Diagnostic{return fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:code,Message:message}}
func(s relationStatsScenario) warnings(extra string,want ...fixture.Diagnostic){s.t.Helper();*s.notices=nil;s.restore(extra,false);if !reflect.DeepEqual(*s.notices,want){s.t.Fatalf("warnings got %#v want %#v",*s.notices,want)}}
