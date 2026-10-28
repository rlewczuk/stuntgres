package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func ltreeArraysPresentation(t *testing.T) {
 fixture.SchemaReferenceCleanup(t,"la_namespace")
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE SCHEMA la_namespace",0))
  run(fixture.Exec("CREATE EXTENSION ltree SCHEMA la_namespace",0))
  run(fixture.Exec("SET search_path TO la_namespace,public",0))
  run(fixture.Exec("CREATE TABLE public.la_identity(paths ltree[],target ltree,q lquery,x ltxtquery)",0))
  run(fixture.Exec("INSERT INTO public.la_identity VALUES ('{a,a.b}','a.b','a.*','a & b')",1))
  run(fixture.Exec("CREATE VIEW public.la_identity_view AS SELECT paths @> target AS a,paths ~ q AS q,paths @ x AS x,paths ?@> target AS first,paths ?~ q AS first_q,paths ?@ x AS first_x FROM public.la_identity",0))
  query:="SELECT paths OPERATOR(la_namespace.@>) target AS v FROM public.la_identity"
  statement,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:query}
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArrayResult(b,query,true))
  literalSQL:="SELECT '{a.b}'::ltree[] ? '{a.*}'::lquery[] AS v"
  literal,err:=c.PrepareContext(p.Ctx,literalSQL);if err!=nil{t.Fatal(err)};defer literal.Close()
  literalHeld:=fixture.HeldStatement{Executor:c,Statement:literal,SQL:literalSQL}
  fixture.RunStep(t,p.Ctx,literalHeld,b.Recorder,ltreeArrayResult(b,literalSQL,true))
  for _,q:=range []string{"ALTER TYPE la_namespace.ltree RENAME TO la_path","ALTER TYPE la_namespace.lquery RENAME TO la_query","ALTER TYPE la_namespace.ltxtquery RENAME TO la_textquery","ALTER TYPE la_namespace.la_path SET SCHEMA public","ALTER TYPE la_namespace.la_query SET SCHEMA public","ALTER TYPE la_namespace.la_textquery SET SCHEMA public"}{run(fixture.Exec(q,0))}
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ltreeArrayResult(b,query,true))
  fixture.RunStep(t,p.Ctx,literalHeld,b.Recorder,ltreeArrayResult(b,literalSQL,true))
  run(ltreeArraySelect(b,"SELECT a,q,x,first,first_q,first_x FROM public.la_identity_view",[]string{"a","q","x","first","first_q","first_x"},[]string{"BOOL","BOOL","BOOL","LTREE","LTREE","LTREE"},[][]any{{true,true,true,"a","a","a.b"}}))
  for _,sql:=range []string{"SELECT paths OPERATOR(la_namespace.?~) q AS v FROM public.la_identity","SELECT la_namespace._ltq_extract_regex(paths,q) AS v FROM public.la_identity"}{run(ltreeArraySelect(b,sql,[]string{"v"},[]string{"LTREE"},[][]any{{"a"}}))}
  for _,sql:=range []string{"SELECT paths OPERATOR(la_namespace.?@) x AS v FROM public.la_identity","SELECT la_namespace._ltxtq_extract_exec(paths,x) AS v FROM public.la_identity"}{run(ltreeArraySelect(b,sql,[]string{"v"},[]string{"LTREE"},[][]any{{"a.b"}}))}
  run(ltreeArrayResult(b,"SELECT la_namespace._ltree_isparent(paths,target) AS v FROM public.la_identity",true))
  run(fixture.Exec("SET search_path TO public",0))
 })
}
