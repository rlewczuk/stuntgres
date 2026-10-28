package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

type ltreePathDeclaration struct { name,signature,result,symbol string }
var ltreePathDeclarations = []ltreePathDeclaration{
 {"subltree","ltree,integer,integer","ltree","subltree"},
 {"subpath","ltree,integer,integer","ltree","subpath"},{"subpath","ltree,integer","ltree","subpath"},
 {"index","ltree,ltree","integer","ltree_index"},{"index","ltree,ltree,integer","integer","ltree_index"},
 {"nlevel","ltree","integer","nlevel"},{"ltree2text","ltree","text","ltree2text"},{"text2ltree","text","ltree","text2ltree"},
 {"lca","ltree[]","ltree","_lca"},{"lca","ltree,ltree","ltree","lca"},
 {"lca","ltree,ltree,ltree","ltree","lca"},{"lca","ltree,ltree,ltree,ltree","ltree","lca"},
 {"lca","ltree,ltree,ltree,ltree,ltree","ltree","lca"},{"lca","ltree,ltree,ltree,ltree,ltree,ltree","ltree","lca"},
 {"lca","ltree,ltree,ltree,ltree,ltree,ltree,ltree","ltree","lca"},{"lca","ltree,ltree,ltree,ltree,ltree,ltree,ltree,ltree","ltree","lca"},
 {"ltree_addltree","ltree,ltree","ltree","ltree_addltree"},{"ltree_addtext","ltree,text","ltree","ltree_addtext"},{"ltree_textadd","text,ltree","ltree","ltree_textadd"},
}
func ltreePathCatalogChecks(schema string) []fixture.Step {
 ns := "('"+schema+"'::regnamespace)::oid"; ext := "(SELECT oid FROM pg_extension WHERE extname='ltree')";owner := "(SELECT extowner FROM pg_extension WHERE extname='ltree')"
 signature := func(s string)string{return strings.ReplaceAll(s,"ltree",schema+".ltree")}
 proc := func(tc ltreePathDeclaration)string{return "('"+schema+"."+tc.name+"("+signature(tc.signature)+")'::regprocedure)::oid"}
 var steps []fixture.Step
 for _,tc := range ltreePathDeclarations {
  result := tc.result;if result=="ltree"{result=schema+".ltree"}
  q:="SELECT proname,prorettype=('"+result+"'::regtype)::oid AS result,pronargs,proisstrict,provolatile,proparallel,proretset,pronamespace="+ns+" AS namespace,proowner="+owner+" AS owner,prolang=13::oid AS c_language,prosrc,probin FROM pg_proc WHERE oid="+proc(tc)
  steps=append(steps,fixture.Select(q,[]string{"proname","result","pronargs","proisstrict","provolatile","proparallel","proretset","namespace","owner","c_language","prosrc","probin"},[]string{"NAME","BOOL","INT2","BOOL","CHAR","CHAR","BOOL","BOOL","BOOL","BOOL","TEXT","TEXT"},[][]any{{tc.name,true,int64(strings.Count(tc.signature,",")+1),true,"i","s",false,true,true,true,tc.symbol,"$libdir/ltree"}}))
  steps=append(steps,fixture.Select("SELECT refobjid="+ext+" AS extension FROM pg_depend WHERE classid=1255 AND objid="+proc(tc)+" AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'",[]string{"extension"},[]string{"BOOL"},[][]any{{true}}))
 }
 for _,tc:=range ltreePathDeclarations[16:] {
  types:=strings.Split(signature(tc.signature),",");op:="('"+schema+".||("+signature(tc.signature)+")'::regoperator)::oid"
  q:="SELECT oprname,oprcode::oid="+proc(tc)+" AS backer,oprcom=0::oid AS commutator,oprnegate=0::oid AS negator,oprleft=('"+types[0]+"'::regtype)::oid AS leftarg,oprright=('"+types[1]+"'::regtype)::oid AS rightarg,oprresult=('"+schema+".ltree'::regtype)::oid AS result,oprnamespace="+ns+" AS namespace,oprowner="+owner+" AS owner,oprcanhash,oprcanmerge,oprrest::oid=0::oid AS restrictor,oprjoin::oid=0::oid AS joiner FROM pg_operator WHERE oid="+op
  steps=append(steps,fixture.Select(q,[]string{"oprname","backer","commutator","negator","leftarg","rightarg","result","namespace","owner","oprcanhash","oprcanmerge","restrictor","joiner"},[]string{"NAME","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{"||",true,true,true,true,true,true,true,true,false,false,true,true}}))
  steps=append(steps,fixture.Select("SELECT refobjid="+ext+" AS extension FROM pg_depend WHERE classid=2617 AND objid="+op+" AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'",[]string{"extension"},[]string{"BOOL"},[][]any{{true}}))
 }
 return steps
}
func TestReferenceLtreePathIdentity(t *testing.T) {
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c)
  for _,s:=range ltreePathCatalogChecks("public"){fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT ('subpath(ltree,integer)'::regprocedure)::oid > 0::oid AS valid,('subpath(ltree,integer)'::regprocedure)::oid <> ('subpath(ltree,integer,integer)'::regprocedure)::oid AS distinct",[]string{"valid","distinct"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
 })
}
