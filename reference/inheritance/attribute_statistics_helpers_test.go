package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

type attributeStatsScenario struct{
 t *testing.T
 p *fixture.Pair
 b fixture.Backend
 conn *sql.Conn
 db *sql.DB
 notices *[]fixture.Diagnostic
 prepared bool
}
func withAttributeStats(t *testing.T,body func(attributeStatsScenario)){
 t.Helper()
 for _,prepared:=range []bool{false,true}{t.Run(fmt.Sprint(prepared),func(t *testing.T){p:=fixture.NewScenario(t);for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){var notices []fixture.Diagnostic;db,conn,recorder:=fixture.NoticeReferencePool(t,p,b.PG,&notices);b.Recorder=recorder;body(attributeStatsScenario{t:t,p:p,b:b,conn:conn,db:db,notices:&notices,prepared:prepared})})}})}
}
func(s attributeStatsScenario) run(step fixture.Step){
 s.t.Helper()
 before:=len(*s.notices)
 defer func(){if s.t.Failed()&&len(*s.notices)>before{s.t.Logf("attribute statistics notices from failed step: %#v",(*s.notices)[before:])}}()
 if s.prepared{step=fixture.Prepared(step)}
 fixture.RunStep(s.t,s.p.Ctx,s.conn,s.b.Recorder,step)
}
func(s attributeStatsScenario) exec(q string){s.run(fixture.Exec(q,0))}
func(s attributeStatsScenario) restore(extra string,want bool){s.t.Helper();s.run(fixture.Select(attributeStatsRestore("a",false,extra),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{want}}))}
func attributeStatsRestore(column string,inherited bool,extra string)string{
 if extra!=""{extra=","+extra}
 return fmt.Sprintf("SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attname','%s','inherited',%t%s)",column,inherited,extra)
}
func(s attributeStatsScenario) scalars(rows ...[]any){s.run(fixture.Select("SELECT attname,inherited,null_frac,avg_width,n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' ORDER BY attname,inherited",[]string{"attname","inherited","null_frac","avg_width","n_distinct"},[]string{"NAME","BOOL","FLOAT4","INT4","FLOAT4"},rows))}
func(s attributeStatsScenario) clear(column string,inherited bool){s.run(fixture.Select(fmt.Sprintf("SELECT pg_clear_attribute_stats('public','ast_t','%s',%t)",column,inherited),[]string{"pg_clear_attribute_stats"},[]string{"VOID"},[][]any{{""}}))}
func(s attributeStatsScenario) fail(q,code,message string){s.t.Helper();s.run(fixture.ImplicitError(q,fixture.ControlsReferenceDiagnostic(code,message)))}
func(s attributeStatsScenario) warnings(extra string,want ...fixture.Diagnostic){s.t.Helper();*s.notices=nil;s.restore(extra,false);if !reflect.DeepEqual(*s.notices,want){s.t.Fatalf("warnings: got %#v want %#v",*s.notices,want)}}
func attributeStatsWarning(code,message,detail string)fixture.Diagnostic{return fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:code,Message:message,Detail:detail}}
