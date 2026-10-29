package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

const fsmScalar="SELECT fsm_part.fsm_page_contents($1)"
const fsmSource="SELECT * FROM fsm_part.fsm_page_contents($1)"
func fsmStep(query string,raw []byte,want any)fixture.Step{var input any=raw;if raw==nil{input=nil};return fixture.Select(query,[]string{"fsm_page_contents"},[]string{"TEXT"},[][]any{{want}},input)}
func fsmBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
func fsmCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)){
 t.Helper();fixture.SchemaReferenceCleanup(t,"fsm_part","fsm_moved");p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){c,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
  if b.PG{clean:=func(){for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS fsm_part CASCADE","DROP SCHEMA IF EXISTS fsm_moved CASCADE"}{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Error(q,err)}}};clean();defer clean()}
  for _,q:=range []string{"CREATE SCHEMA fsm_part","CREATE EXTENSION pageinspect SCHEMA fsm_part"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))};body(t,p,b,c)
 })}
}
