package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeLtxtqueryCatalogChecks(schema string) []fixture.Step {
 typ:=schema+".ltree"
 query:=schema+".ltxtquery"
 signature:=func(i int)string{if i==0{return typ+","+query};return query+","+typ}
 ns:="('"+schema+"'::regnamespace)::oid"
 owner:="(SELECT extowner FROM pg_extension WHERE extname='ltree')"
 ext:="(SELECT oid FROM pg_extension WHERE extname='ltree')"
 proc:=func(i int)string{return "('"+schema+"."+ltreeLtxtqueryCases[i].name+"("+signature(i)+")'::regprocedure)::oid"}
 op:=func(i int)string{return "('"+schema+".@("+signature(i)+")'::regoperator)::oid"}
 var steps []fixture.Step
 for i,tc:=range ltreeLtxtqueryCases {
  left,right:=typ,query;if i==1{left,right=right,left}
  steps=append(steps,fixture.Select("SELECT proname,prorettype=16::oid AS result,pronargs,proisstrict,provolatile,proparallel,pronamespace="+ns+" AS namespace,proowner="+owner+" AS owner,prolang=13::oid AS c_language,prosrc,probin FROM pg_proc WHERE oid="+proc(i),
   []string{"proname","result","pronargs","proisstrict","provolatile","proparallel","namespace","owner","c_language","prosrc","probin"},
   []string{"NAME","BOOL","INT2","BOOL","CHAR","CHAR","BOOL","BOOL","BOOL","TEXT","TEXT"},
   [][]any{{tc.name,true,int64(2),true,"i","s",true,true,true,tc.name,"$libdir/ltree"}}))
  steps=append(steps,fixture.Select("SELECT oprname,oprcode::oid="+proc(i)+" AS backer,oprcom="+op(i^1)+" AS commutator,oprnegate=0::oid AS negator,oprleft=('"+left+"'::regtype)::oid AS leftarg,oprright=('"+right+"'::regtype)::oid AS rightarg,oprresult=16::oid AS result,oprnamespace="+ns+" AS namespace,oprowner="+owner+" AS owner,oprcanhash,oprcanmerge,oprrest::oid=5040::oid AS restrictor,oprjoin::oid=5041::oid AS joiner FROM pg_operator WHERE oid="+op(i),
   []string{"oprname","backer","commutator","negator","leftarg","rightarg","result","namespace","owner","oprcanhash","oprcanmerge","restrictor","joiner"},
   []string{"NAME","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},
   [][]any{{tc.op,true,true,true,true,true,true,true,true,false,false,true,true}}))
  steps=append(steps,fixture.Select("SELECT oprrest::oid='pg_catalog.matchingsel'::regproc::oid AS restrictor_identity,oprjoin::oid='pg_catalog.matchingjoinsel'::regproc::oid AS joiner_identity FROM pg_operator WHERE oid="+op(i),[]string{"restrictor_identity","joiner_identity"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
  for _,member:=range []struct{class,identity string}{{"1255",proc(i)},{"2617",op(i)}}{
   steps=append(steps,fixture.Select("SELECT refobjid="+ext+" AS extension FROM pg_depend WHERE classid="+member.class+" AND objid="+member.identity+" AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'",[]string{"extension"},[]string{"BOOL"},[][]any{{true}}))
  }
 }
 return steps
}
func TestReferenceLtreeLtxtqueryIdentity(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  ltreeInstall(t,p,b,c)
  for _,s:=range ltreeLtxtqueryCatalogChecks("public"){fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
  for _,q:=range []string{
   "SELECT ('ltxtq_exec(ltree,ltxtquery)'::regprocedure)::oid > 0::oid AS valid",
   "SELECT ('ltxtq_exec(ltree,ltxtquery)'::regprocedure)::oid <> ('ltxtq_rexec(ltxtquery,ltree)'::regprocedure)::oid AS valid",
  } {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(q,[]string{"valid"},[]string{"BOOL"},[][]any{{true}}))}
 })
}
