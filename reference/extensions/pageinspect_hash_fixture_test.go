package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)
func hashCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)){
 t.Helper();fixture.SchemaReferenceCleanup(t,"hash_part","hash_moved")
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  c,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
  if b.PG{clean:=func(){for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS hash_part CASCADE","DROP SCHEMA IF EXISTS hash_moved CASCADE"}{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Error(q,err)}}};clean();defer clean()}
  for _,q:=range []string{"CREATE SCHEMA hash_part","CREATE EXTENSION pageinspect SCHEMA hash_part"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  body(t,p,b,c)
 })}
}
func hashQuery(i int)string{q:="SELECT * FROM hash_part."+fixture.HashMembers[i]+"($1::bytea)";if i==2{q+=" ORDER BY itemoffset"};return q}
func hashStep(i int,raw []byte,rows [][]any)fixture.Step{return fixture.Select(hashQuery(i),fixture.HashNames[i],fixture.HashTypes[i],rows,raw)}
func hashBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step){t.Helper();fixture.BRINBoth(t,p,b,c,fixture.HashMetadata(s,b.PG,false))}
func hashError(i int,raw []byte,code,message,detail string)fixture.Step{s:=fixture.ImplicitError(hashQuery(i),fixture.Diagnostic{Code:code,Message:message,Detail:detail},raw);s.SelectRows=true;s.ErrorStage="execute";return s}
func hashMissing(query,signature string,args ...any)fixture.Step{return fixture.ImplicitError(query,fixture.Diagnostic{Code:"42883",Message:"function "+signature+" does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(strings.Index(query,"hash_part.")+1)},args...)}
