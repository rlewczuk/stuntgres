package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeHierarchyCatalogChecks(schema string) []fixture.Step {
 typ:=schema+".ltree"
 ns:="('"+schema+"'::regnamespace)::oid"
 owner:="(SELECT extowner FROM pg_extension WHERE extname='ltree')"
 ext:="(SELECT oid FROM pg_extension WHERE extname='ltree')"
 proc:=func(name string)string{return "('"+schema+"."+name+"("+typ+","+typ+")'::regprocedure)::oid"}
 op:=func(name string)string{return "('"+schema+"."+name+"("+typ+","+typ+")'::regoperator)::oid"}
 var steps []fixture.Step
 for i,tc:=range ltreeHierarchyCases {
  steps=append(steps,fixture.Select("SELECT proname,prorettype=16::oid AS result,pronargs,proisstrict,provolatile,proparallel,pronamespace="+ns+" AS namespace,proowner="+owner+" AS owner,prolang=13::oid AS c_language,prosrc,probin FROM pg_proc WHERE oid="+proc(tc.name),
   []string{"proname","result","pronargs","proisstrict","provolatile","proparallel","namespace","owner","c_language","prosrc","probin"},
   []string{"NAME","BOOL","INT2","BOOL","CHAR","CHAR","BOOL","BOOL","BOOL","TEXT","TEXT"},
   [][]any{{tc.name,true,int64(2),true,"i","s",true,true,true,tc.name,"$libdir/ltree"}}))
  steps=append(steps,fixture.Select("SELECT oprname,oprcode::oid="+proc(tc.name)+" AS backer,oprcom="+op(ltreeHierarchyCases[i^1].op)+" AS commutator,oprnegate=0::oid AS negator,oprleft=('"+typ+"'::regtype)::oid AS leftarg,oprright=oprleft AS rightarg,oprresult=16::oid AS result,oprnamespace="+ns+" AS namespace,oprowner="+owner+" AS owner,oprcanhash,oprcanmerge,oprrest::oid=5040::oid AS restrictor,oprjoin::oid=5041::oid AS joiner FROM pg_operator WHERE oid="+op(tc.op),
   []string{"oprname","backer","commutator","negator","leftarg","rightarg","result","namespace","owner","oprcanhash","oprcanmerge","restrictor","joiner"},
   []string{"NAME","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},
   [][]any{{tc.op,true,true,true,true,true,true,true,true,false,false,true,true}}))
  for _,member:=range []struct{class,identity string}{{"1255",proc(tc.name)},{"2617",op(tc.op)}}{
   steps=append(steps,fixture.Select("SELECT refobjid="+ext+" AS extension FROM pg_depend WHERE classid="+member.class+" AND objid="+member.identity+" AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'",[]string{"extension"},[]string{"BOOL"},[][]any{{true}}))
  }
 }
 return steps
}
func TestReferenceLtreeHierarchyCatalog(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  ltreeInstall(t,p,b,c)
  for _,s:=range ltreeHierarchyCatalogChecks("public"){fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
  for _,q:=range []string{
   "SELECT ('ltree_isparent(ltree,ltree)'::regprocedure)::oid > 0::oid AS valid",
   "SELECT ('ltree_isparent(ltree,ltree)'::regprocedure)::oid <> ('ltree_risparent(ltree,ltree)'::regprocedure)::oid AS valid",
  } {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(q,[]string{"valid"},[]string{"BOOL"},[][]any{{true}}))}
 })
}
