package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

const btQuery="SELECT * FROM bt_part.bt_page_items($1) ORDER BY itemoffset"
func btStep(page []byte,rows [][]any)fixture.Step{return fixture.Select(btQuery,fixture.BTNames,fixture.BTTypes,rows,page)}
func btBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
func btCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)){
 t.Helper();fixture.SchemaReferenceCleanup(t,"bt_part","bt_moved");p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){c,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
  if b.PG{clean:=func(){for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS bt_part CASCADE","DROP SCHEMA IF EXISTS bt_moved CASCADE"}{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Error(q,err)}}};clean();defer clean()}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SCHEMA bt_part",0));fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE EXTENSION pageinspect SCHEMA bt_part",0));body(t,p,b,c)
 })}
}
