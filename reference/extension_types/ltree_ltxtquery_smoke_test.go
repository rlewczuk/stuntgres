package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreeLtxtqueryFeatureSmoke(t *testing.T){
 t.Run("projection_filter_errors",func(t *testing.T){
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeInstall(t,p,b,c);ltreeLtxtquerySeed(t,p,b,c)
   for _,s:=range ltreeLtxtqueryChecks(){ltreeLtxtqueryRun(t,p,b,c,s)}
   for _,tc:=range []struct{q string;want any}{
    {"SELECT 'Top.Blocked'::ltree @ 'Top | Europe & !Blocked'::ltxtquery AS v",true},
    {"SELECT 'Top.Blocked'::ltree @ '(Top | Europe) & !Blocked'::ltxtquery AS v",false},
    {"SELECT 'FOO1_BAR2_baz'::ltree @ 'foo_bar%@*'::ltxtquery AS v",true},
    {"SELECT ''::ltree @ '!foo'::ltxtquery AS v",true},
    {"SELECT NULL::ltree @ '!foo'::ltxtquery AS v",nil},
   }{ltreeLtxtqueryRun(t,p,b,c,ltreeArrayResult(b,tc.q,tc.want))}
   q:="SELECT $1::ltree @ $2::ltxtquery AS forward,$2::ltxtquery @ $1::ltree AS reverse"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   ltreeLtxtqueryRun(t,p,b,held,ltreeLtxtqueryPair(q,"Europe.Russia","Europe & Russia",true,true))
   want:=queryIntParameterDiagnostic("42601","modifiers syntax error","foo@bar",2)
   s:=fixture.ErrorDiagnostic(q,want,"Europe.Russia","foo@bar");s.SelectRows=true;s.ErrorStage="execute";ltreeLtxtqueryRun(t,p,b,held,s)
   ltreeLtxtqueryHeldChecks(t,p,b,held)
   ltreeLtxtqueryMismatch(t,p,b,c)
  })
 })
 t.Run("restored_invocation",func(t *testing.T){
  fixture.SchemaReferenceCleanup(t,ltreeLtxtquerySchema)
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   ltreeLtxtqueryControlSetup(t,p,b,c)
   q:="SELECT $1::text::lxq_provider.ltree OPERATOR(lxq_provider.@) $2::text::lxq_provider.ltxtquery AS forward,$2::text::lxq_provider.ltxtquery OPERATOR(lxq_provider.@) $1::text::lxq_provider.ltree AS reverse"
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q};ltreeLtxtqueryHeldChecks(t,p,b,held)
   if b.PG{ltreeLtxtqueryControlChecks(t,p,b,c);return}
   dump:=ltreeLtxtqueryDump(t,p);rows,columns:=ltreeLtxtqueryRetain(t,p,b)
   if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
   if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
   ltreeLtxtqueryControlChecks(t,p,b,c);ltreeLtxtqueryHeldChecks(t,p,b,held);ltreeLtxtqueryConsume(t,rows,columns);ltreeLtxtqueryReplay(t,p,dump)
  })
 })
}
