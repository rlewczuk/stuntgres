package reference_test

import (
 "bytes"
 "stuntgres"
 "database/sql"
 "regexp"
 "testing"
 "strings"
 "stuntgres/reference/fixture"
)

func ltreeLtxtqueryPresentation(t *testing.T){
 fixture.SchemaReferenceCleanup(t,"lxq_names")
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){ltreeLtxtqueryRun(t,p,b,c,s)}
  for _,q:=range []string{"CREATE SCHEMA lxq_names","CREATE EXTENSION ltree SCHEMA lxq_names","SET search_path=public,lxq_names","CREATE TABLE public.lxq_named(path ltree,query ltxtquery,source text)","INSERT INTO public.lxq_named VALUES ('Europe.Russia','Europe & Russia','Europe & Russia')","CREATE VIEW public.lxq_named_view AS SELECT path @ query AS forward,query @ path AS reverse,ltxtq_exec(path,query) AS exec,ltxtq_rexec(query,path) AS rexec FROM public.lxq_named"}{count:=int64(0);if strings.HasPrefix(q,"INSERT INTO"){count=1};run(fixture.Exec(q,count))}
  run(fixture.Exec("CREATE DOMAIN public.lxq_query_domain AS lxq_names.ltxtquery NOT NULL",0))
  run(fixture.Exec("CREATE VIEW public.lxq_cast_view AS SELECT path @ 'Europe & Russia'::ltxtquery AS plain,path @ CAST(CAST('Europe & Russia' AS ltxtquery) AS ltxtquery) AS nested,CAST(query::text AS ltxtquery) @ path AS converted,ltxtq_exec(path,CAST(query::text AS public.lxq_query_domain)) AS domain_query,CAST(source AS ltxtquery) @ path AS converted_text FROM public.lxq_named",0))
  q:="SELECT path OPERATOR(lxq_names.@) query AS forward,query OPERATOR(lxq_names.@) path AS reverse FROM public.lxq_named"
  statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer statement.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
  check:=fixture.Select(q,[]string{"forward","reverse"},[]string{"BOOL","BOOL"},[][]any{{true,true}});ltreeLtxtqueryRun(t,p,b,held,check)
  for _,q:=range []string{"ALTER TYPE lxq_names.ltree RENAME TO lxq_path","ALTER TYPE lxq_names.ltxtquery RENAME TO lxq_query","ALTER TYPE lxq_names.lxq_path SET SCHEMA public","ALTER TYPE lxq_names.lxq_query SET SCHEMA public","SET search_path=public"}{run(fixture.Exec(q,0))}
  ltreeLtxtqueryRun(t,p,b,held,check)
  verify:=func(ex fixture.Executor,backend fixture.Backend){
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Select("SELECT forward,reverse,exec,rexec FROM public.lxq_named_view",[]string{"forward","reverse","exec","rexec"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true}}))
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Select("SELECT plain,nested,converted,domain_query,converted_text FROM public.lxq_cast_view",[]string{"plain","nested","converted","domain_query","converted_text"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true}}))
   ltreeLtxtqueryCastPresentation(t,p,backend,ex)
   // Row-dependent casts must still execute, not merely print correctly.
   // Neither failure can be explained by the already-valid stored query value.
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Exec("UPDATE public.lxq_named SET query=NULL",1))
   domain:=fixture.ControlsReferenceDiagnostic("23502","domain lxq_query_domain does not allow null values")
   domain.Schema,domain.DataType="public","lxq_query_domain"
   badDomain:=fixture.ErrorDiagnostic("SELECT domain_query FROM public.lxq_cast_view",domain)
   badDomain.ErrorStage="execute"
   ltreeLtxtqueryRun(t,p,backend,ex,badDomain)
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Exec("UPDATE public.lxq_named SET query='Europe & Russia'",1))
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Exec("UPDATE public.lxq_named SET source='foo@bar'",1))
   badText:=fixture.ErrorDiagnostic("SELECT converted_text FROM public.lxq_cast_view",fixture.ControlsReferenceDiagnostic("42601","modifiers syntax error"))
   badText.ErrorStage="execute"
   ltreeLtxtqueryRun(t,p,backend,ex,badText)
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Exec("UPDATE public.lxq_named SET source='Europe & Russia'",1))
   ltreeLtxtqueryRun(t,p,backend,ex,fixture.Select("SELECT domain_query,converted_text FROM public.lxq_cast_view",[]string{"domain_query","converted_text"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
   for _,q:=range []string{"SELECT lxq_names.ltxtq_exec(path,query) AS v FROM public.lxq_named","SELECT lxq_names.ltxtq_rexec(query,path) AS v FROM public.lxq_named","SELECT 'Europe.Russia'::public.lxq_path OPERATOR(lxq_names.@) 'Europe & Russia'::public.lxq_query AS v","SELECT 'Europe & Russia'::public.lxq_query OPERATOR(lxq_names.@) 'Europe.Russia'::public.lxq_path AS v"}{s:=ltreeArrayResult(backend,q,true);ltreeLtxtqueryRun(t,p,backend,ex,s);ltreeLtxtqueryRun(t,p,backend,ex,fixture.Prepared(s))}
  }
  verify(c,b)
  if !b.PG{
   dump:=ltreeLtxtqueryDump(t,p)
   ltreeLtxtqueryCastDump(t,dump)
   for cycle:=1;cycle<=2;cycle++{
    if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
    if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
    verify(c,b)
    got:=ltreeLtxtqueryDump(t,p)
    ltreeLtxtqueryCastDump(t,got)
    if !bytes.Equal(got,dump){t.Fatalf("mixed cast replay cycle %d changed deterministic dump\nbefore:\n%s\nafter:\n%s",cycle,dump,got)}
   }
   if p.Postgres!=nil{
    clean:="DROP SCHEMA IF EXISTS lxq_names CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
    if _,err:=p.Postgres.ExecContext(p.Ctx,clean);err!=nil{t.Fatal(err)}
    defer func(){if _,err:=p.Postgres.ExecContext(p.Ctx,clean);err!=nil{t.Error(err)}}()
    if _,err:=p.Postgres.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatal("renamed mixed-call dump replay",err)}
    pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord};verify(pg.DB,pg)
   }
  }
 })
}

// Inspect the public view presentation, not the implementation's expression
// graph. Expectations are fixed before transport and reused after each Restore
// and the replay of the actual native SQL on the release oracle.
func ltreeLtxtqueryCastPresentation(t *testing.T,p *fixture.Pair,b fixture.Backend,ex fixture.Executor){
 t.Helper()
 // Pretty presentation exposes :: chains. PostgreSQL discards the redundant
 // outer same-type cast during analysis; native stored syntax retains it.
 nested:=`'Europe & Russia'::lxq_query\)::lxq_query`
 converted:=`"?query"?::text\)::lxq_query`
 domain:=`"?query"?::text\)?::lxq_query_domain`
 if b.PG{
  nested=`'Europe & Russia'::lxq_query AS nested`
  converted=`query::text::lxq_query OPERATOR`
  domain=`query::text::lxq_query_domain::lxq_query`
 }
 patterns:=[]string{
  `'Europe & Russia'::lxq_query\)? AS plain`,
  nested,
  converted,
  domain,
  `"?source"?::lxq_query`,
 }
 rows,err:=ex.QueryContext(p.Ctx,"SELECT pg_get_viewdef('public.lxq_cast_view'::regclass,true) AS definition")
 if err!=nil{t.Fatal(err)}
 defer rows.Close()
 columns,err:=rows.Columns()
 if err!=nil{t.Fatal(err)}
 if len(columns)!=1 || columns[0]!="definition"{t.Fatalf("public view presentation columns: %v",columns)}
 if !rows.Next(){t.Fatalf("missing public cast-view definition: %v",rows.Err())}
 var definition string
 if err:=rows.Scan(&definition);err!=nil{t.Fatal(err)}
 for _,pattern:=range patterns{
  if !regexp.MustCompile(pattern).MatchString(definition){t.Fatalf("public cast-view definition missing %q:\n%s",pattern,definition)}
 }
 if rows.Next(){t.Fatal("multiple public cast-view definitions")}
 if err:=rows.Err();err!=nil{t.Fatal(err)}
}

func ltreeLtxtqueryCastDump(t *testing.T,dump []byte){
 t.Helper()
 // Unlike PostgreSQL's analyzed presentation, Dump promises to retain the
 // stored outer cast. Independently check the FIRST dump too: byte stability
 // alone would accept a cast lost before the first Restore.
 column:=`"[a-z_][a-z_0-9]*"\."query"`
 queryType:=`"public"\."lxq_query"`
 for _,pattern:=range []string{
  `CAST\(CAST\('Europe & Russia' AS `+queryType+`\) AS `+queryType+`\)`,
  `CAST\(CAST\(`+column+` AS TEXT\) AS `+queryType+`\)`,
  `CAST\(CAST\(`+column+` AS TEXT\) AS "public"\."lxq_query_domain"\)`,
  `CAST\("[a-z_][a-z_0-9]*"\."source" AS `+queryType+`\)`,
 }{
  if !regexp.MustCompile(pattern).Match(dump){t.Fatalf("public dump missing retained cast chain %q:\n%s",pattern,dump)}
 }
}
