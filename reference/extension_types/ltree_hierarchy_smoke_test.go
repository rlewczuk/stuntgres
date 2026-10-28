package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeHierarchyFeatureSmoke(t *testing.T){
 t.Run("projection_filter_errors",func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c);ltreeHierarchySeed(t,p,b,c)
   for _,s:=range ltreeHierarchyChecks(){ltreeHierarchyRun(t,p,b,c,s)}
   q:="SELECT $1::ltree @> $2::ltree AS ancestor,$1::ltree <@ $2::ltree AS descendant"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   ltreeHierarchyHeldChecks(t,p,b,held)
   want:=queryIntParameterDiagnostic("42601","ltree syntax error at character 3","a..b",1)
   s:=fixture.ErrorDiagnostic(q,want,"a..b","Top");s.SelectRows=true;s.ErrorStage="execute"
   ltreeHierarchyRun(t,p,b,held,s)
   ltreeHierarchyRun(t,p,b,held,ltreeHierarchyPair(q,"Top","Top.Child",true,false))
   ltreeHierarchyMismatch(t,p,b,c)
  })
 })
 t.Run("restored_invocation",func(t *testing.T){
  fixture.SchemaReferenceCleanup(t,ltreeHierarchySchema)
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeHierarchyControlSetup(t,p,b,c)
   q:="SELECT $1::text::ltree @> $2::text::ltree AS ancestor,$1::text::ltree <@ $2::text::ltree AS descendant"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   ltreeHierarchyHeldChecks(t,p,b,held)
   if b.PG {ltreeHierarchyControlChecks(t,p,b,c);return}
   dump:=ltreeHierarchyDump(t,p)
   rows,columns:=ltreeHierarchyRetain(t,p,b)
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
   ltreeHierarchyControlChecks(t,p,b,c)
   ltreeHierarchyHeldChecks(t,p,b,held)
   ltreeHierarchyConsume(t,rows,columns)
   ltreeHierarchyReplay(t,p,dump)
  })
 })
}
