package reference_test

import (
 "database/sql"
 "testing"
 "time"

 "stuntgres/reference/fixture"
)

// Expectations are independent of the implementation: REL_18_6 contrib/spi/moddatetime.c.
func moddatetimeCase(t *testing.T, body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {
 t.Helper()
 fixture.SchemaReferenceCleanup(t,"md_saved")
 fixture.SchemaReferenceCleanup(t,"md_other")
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T){
  c,err:=b.DB.Conn(p.Ctx); if err!=nil {t.Fatal(err)}; defer c.Close()
  if b.PG { clean:=func(){for _,q:=range []string{"ROLLBACK","RESET TimeZone","RESET DateStyle","RESET search_path","DROP EXTENSION IF EXISTS moddatetime CASCADE","DROP SCHEMA IF EXISTS md_saved CASCADE","DROP SCHEMA IF EXISTS md_other CASCADE","DROP TABLE IF EXISTS mdt CASCADE","DROP TABLE IF EXISTS md_chain CASCADE","DROP FUNCTION IF EXISTS public.md_observe() CASCADE","DROP FUNCTION IF EXISTS public.moddatetime() CASCADE","DROP DOMAIN IF EXISTS md_domain CASCADE"} {if _,err:=c.ExecContext(p.Ctx,q);err!=nil {t.Error(q,err)}}}; clean(); defer clean() }
  body(t,p,b,c)
 }) }
}
func mdExec(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,queries ...string) { t.Helper(); for _,q:=range queries {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))} }
func mdRun(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step) {t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
func mdTimes(t *testing.T,p *fixture.Pair,c *sql.Conn)(time.Time,time.Time) {
 t.Helper()
 var civil,zoned time.Time
 if err:=c.QueryRowContext(p.Ctx,"SELECT localtimestamp,transaction_timestamp()").Scan(&civil,&zoned);err!=nil {t.Fatal(err)}
 // RunStep compares normalized carriers. TIMESTAMP preserves civil fields,
 // whereas TIMESTAMPTZ preserves the instant, independent of session location.
 civil=time.Date(civil.Year(),civil.Month(),civil.Day(),civil.Hour(),civil.Minute(),civil.Second(),civil.Nanosecond(),time.UTC)
 return civil,zoned.Round(0).UTC()
}
func mdRows(q string,rows ...[]any) fixture.Step {return fixture.Select(q,[]string{"id","stamp","zoned"},[]string{"INT4","TIMESTAMP","TIMESTAMPTZ"},rows)}
func mdStamp(q string,rows ...[]any) fixture.Step {return fixture.Select(q,[]string{"id","stamp"},[]string{"INT4","TIMESTAMP"},rows)}
func mdError(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,q,code,message string) {
 t.Helper();for _,prepared:=range []bool{false,true} {s:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic(code,message));s.Prepared=prepared;s.ErrorStage="execute";mdRun(t,p,b,c,s)}
}
