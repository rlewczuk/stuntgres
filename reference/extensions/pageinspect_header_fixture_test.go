package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

const headerScalar="SELECT header_part.page_header($1)"
const headerSource="SELECT * FROM header_part.page_header($1)"
const checksumScalar="SELECT header_part.page_checksum($1,$2)"
const checksumSource="SELECT * FROM header_part.page_checksum($1,$2)"

func headerCase(t *testing.T,install bool,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {
 t.Helper();fixture.SchemaReferenceCleanup(t,"header_part","header_moved");p:=fixture.NewScenario(t)
 for _,b:=range p.Backends() {
  t.Run(b.Name,func(t *testing.T){
   c,e:=b.DB.Conn(p.Ctx);if e!=nil {t.Fatal(e)};defer c.Close()
   if b.PG {clean:=func(){for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS header_part CASCADE","DROP SCHEMA IF EXISTS header_moved CASCADE"} {if _,e:=c.ExecContext(p.Ctx,q);e!=nil {t.Error(q,e)}}};clean();defer clean()}
   if install {for _,q:=range []string{"CREATE SCHEMA header_part","CREATE EXTENSION pageinspect SCHEMA header_part"} {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}}
   body(t,p,b,c)
  })
 }
}
func headerBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step) {
 t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
}
func headerStep(query string,raw any,row []any) fixture.Step {
 return fixture.Select(query,fixture.HeaderNames,fixture.HeaderTypes,[][]any{row},raw)
}
func checksumStep(query string,raw,block,want any) fixture.Step {
 return fixture.Select(query,[]string{"page_checksum"},[]string{"INT2"},[][]any{{want}},raw,block)
}
func headerError(query,code,message,detail string,args ...any) fixture.Step {
 s:=fixture.ImplicitError(query,fixture.Diagnostic{Code:code,Message:message,Detail:detail},args...);s.SelectRows=true;s.ErrorStage="execute";return s
}
func headerHeld(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,query string,steps []fixture.Step) {
 t.Helper();st,e:=c.PrepareContext(p.Ctx,query);if e!=nil {t.Fatal(e)};defer st.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:query}
 for _,s:=range steps {headerBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
}
