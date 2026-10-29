package reference_test

import (
 "database/sql"
 "encoding/binary"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func ginRelocated(p []byte) []byte {
 copy(p[4088:4096],p[8184:8192]);clear(p[8184:8192])
 binary.LittleEndian.PutUint16(p[16:],4088)
 binary.LittleEndian.PutUint16(p[18:],0x1004)
 return p
}
func ginOrdinary(i int) []byte {switch i {case 0:return fixture.GINMeta();case 1:return fixture.GINPage(0xffff);default:return fixture.GINPosting()}}
func ginOrdinaryRows(i int) [][]any {return [][][]any{fixture.GINMetaRows,fixture.GINOpaqueRows,fixture.GINLeafRows}[i]}
func ginQuery(i int) string {q:="SELECT * FROM gin_part."+fixture.GINMembers[i]+"($1)";if i==2 {q+=" ORDER BY first_tid"};return q}
func ginStep(i int,p []byte,rows [][]any) fixture.Step {return fixture.Select(ginQuery(i),fixture.GINNames[i],fixture.GINTypes[i],rows,p)}
func ginBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step) {
 t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
}
func ginMissing(query,signature string,args ...any) fixture.Step {
 return fixture.ImplicitError(query,fixture.Diagnostic{Code:"42883",Message:"function "+signature+" does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(strings.Index(query,"gin_part.")+1)},args...)
}
func ginCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {t.Helper();ginCaseSetup(t,true,body)}
func ginCaseSetup(t *testing.T,install bool,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {
 t.Helper();fixture.SchemaReferenceCleanup(t,"gin_part","gin_moved")
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
  c,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer c.Close()
  if b.PG {
   clean:=func() {for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS gin_part CASCADE","DROP SCHEMA IF EXISTS gin_moved CASCADE"} {if _,err:=c.ExecContext(p.Ctx,q);err!=nil {t.Error(q,err)}}}
   clean();defer clean()
  }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SCHEMA gin_part",0))
  if install {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE EXTENSION pageinspect SCHEMA gin_part",0))}
  body(t,p,b,c)
 })}
}
