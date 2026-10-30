package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformsDeclarations(t *testing.T) {
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  lang:=hostRuntimeLanguage(b)
  source:=transformsLiteral(transformsSource(b,"increment-v1",hostIncrementPython))
  for _,item:=range []struct{name,options string}{
   {"tr_before","TRANSFORM FOR TYPE hstore LANGUAGE "+lang+" AS "+source},
   {"tr_after","AS "+source+" LANGUAGE "+lang+" TRANSFORM FOR TYPE public.hstore"},
   {"tr_duplicate","LANGUAGE "+lang+" TRANSFORM FOR TYPE hstore, FOR TYPE hstore AS "+source},
  } {
   hostExec(t,p,b,c,"CREATE FUNCTION "+item.name+"(v hstore) RETURNS hstore "+item.options)
   n:=int64(1);if item.name=="tr_duplicate"{n=2}
   transformsAssociation(t,p,b,c,item.name,n)
   hostBag(t,p,b,c,item.name+"('a=>7,b=>NULL'::hstore)",transformsMapRows)
  }
  // A genuine association need not occur in any argument or result position.
  python:="return {'a':1,'b':'boo','c':None}"
  hostExec(t,p,b,c,"CREATE FUNCTION tr_noinput() RETURNS hstore TRANSFORM FOR TYPE hstore LANGUAGE "+lang+" AS "+transformsLiteral(transformsSource(b,"emit-v1",python)))
  transformsAssociation(t,p,b,c,"tr_noinput",1)
  hostBag(t,p,b,c,"tr_noinput()",[][]any{{"a","1"},{"b","boo"},{"c",nil}})
  hostExec(t,p,b,c,"CREATE FUNCTION tr_unused() RETURNS integer LANGUAGE "+lang+" TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"one-v1","return 1")))
  transformsPairEdges(t,p,b,c,"tr_unused",1)
 })
}

func TestReferenceRoutineTransformsNormalization(t *testing.T) {
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  hostExec(t,p,b,c,"CREATE DOMAIN tr_scalar_domain AS hstore")
  hostExec(t,p,b,c,"CREATE DOMAIN tr_array_domain AS hstore[]")
  for i,list:=range []string{"FOR TYPE hstore[]","FOR TYPE tr_array_domain","FOR TYPE public.\"hstore\"","FOR TYPE hstore, FOR TYPE hstore[]"}{
   name:=[]string{"tr_array","tr_domain_array","tr_quoted","tr_normal_dup"}[i]
   transformsFunction(t,p,b,c,name,"increment-v1",hostIncrementPython,list,"")
   n:=int64(1);if i==3{n=2}
   transformsAssociation(t,p,b,c,name,n)
   transformsPairEdges(t,p,b,c,name,1)
   hostBag(t,p,b,c,name+"('a=>7,b=>NULL'::hstore)",transformsMapRows)
  }
  q:="CREATE FUNCTION tr_domain_missing(v hstore) RETURNS hstore LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM FOR TYPE tr_scalar_domain AS "+transformsLiteral(transformsSource(b,"copy-v1","return v"))
  transformsDiagnostic(t,p,c,q,"42704","transform for type tr_scalar_domain language \""+hostRuntimeLanguage(b)+"\" does not exist")
 })
}

func TestReferenceRoutineTransformsDiagnostics(t *testing.T) {
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  lang:=hostRuntimeLanguage(b)
  source:=transformsLiteral(transformsSource(b,"copy-v1","return v"))
  for _,item:=range []struct{q,code,message string}{
   {"CREATE FUNCTION tr_missing(v hstore) RETURNS hstore LANGUAGE "+lang+" TRANSFORM FOR TYPE integer AS "+source,"42704","transform for type integer language \""+lang+"\" does not exist"},
   {"CREATE FUNCTION tr_unknown(v hstore) RETURNS hstore LANGUAGE "+lang+" TRANSFORM FOR TYPE no_transform_type AS "+source,"42704","type \"no_transform_type\" does not exist"},
   {"CREATE FUNCTION tr_repeat(v hstore) RETURNS hstore LANGUAGE "+lang+" TRANSFORM FOR TYPE hstore TRANSFORM FOR TYPE hstore AS "+source,"42601","conflicting or redundant options"},
   {"ALTER FUNCTION tr_absent(hstore) TRANSFORM FOR TYPE hstore","42601","syntax error at or near \"TRANSFORM\""},
   {"ALTER PROCEDURE tr_absent(hstore) TRANSFORM FOR TYPE hstore","42601","syntax error at or near \"TRANSFORM\""},
   {"CREATE FUNCTION tr_trailing(v hstore) RETURNS hstore LANGUAGE "+lang+" TRANSFORM FOR TYPE hstore AS "+source+" trailing","42601","syntax error at or near \"trailing\""},
  }{transformsDiagnostic(t,p,c,item.q,item.code,item.message)}
  handler:="go_host_none_call_handler";if b.PG{handler="plpython3_call_handler"}
  defer routineLanguagesCleanup(t,c,"DROP LANGUAGE IF EXISTS tr_no_pair CASCADE")()
  hostExec(t,p,b,c,"CREATE LANGUAGE tr_no_pair HANDLER "+handler)
  transformsDiagnostic(t,p,c,"CREATE FUNCTION tr_wrong_language(v hstore) RETURNS hstore LANGUAGE tr_no_pair TRANSFORM FOR TYPE hstore AS "+source,"42704","transform for type hstore language \"tr_no_pair\" does not exist")
  // The missing transform list type is resolved before the missing signature.
  transformsDiagnostic(t,p,c,"CREATE FUNCTION tr_precedence(v no_argument_type) RETURNS hstore LANGUAGE "+lang+" TRANSFORM FOR TYPE no_transform_type AS "+source,"42704","type \"no_transform_type\" does not exist")
 })
}

func TestReferenceRoutineTransformsPrivileges(t *testing.T) {
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  defer routineLanguagesCleanup(t,c,"RESET ROLE","GRANT USAGE ON TYPE hstore TO PUBLIC","DROP LANGUAGE IF EXISTS tr_trusted CASCADE","DROP OWNED BY tr_reader CASCADE","DROP ROLE tr_reader")()
  handler,from,to:="go_host_call_handler","go_host_from_hstore","go_host_to_hstore"
  if b.PG{handler,from,to="plpython3_call_handler","hstore_to_plpython3","plpython3_to_hstore"}
  for _,q:=range []string{"CREATE ROLE tr_reader","GRANT USAGE,CREATE ON SCHEMA public TO tr_reader","CREATE TRUSTED LANGUAGE tr_trusted HANDLER "+handler}{hostExec(t,p,b,c,q)}
  if b.PG{hostExec(t,p,b,c,"CREATE TRANSFORM FOR hstore LANGUAGE tr_trusted (FROM SQL WITH FUNCTION "+from+"(internal), TO SQL WITH FUNCTION "+to+"(internal))")}
  hostExec(t,p,b,c,"CREATE FUNCTION tr_root_owned(v hstore) RETURNS hstore LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"increment-v1",hostIncrementPython)))
  for _,q:=range []string{"GRANT USAGE ON LANGUAGE tr_trusted TO tr_reader","REVOKE USAGE ON TYPE hstore FROM PUBLIC","REVOKE EXECUTE ON FUNCTION "+from+"(internal),"+to+"(internal) FROM PUBLIC","SET ROLE tr_reader"}{hostExec(t,p,b,c,q)}
  // Return hstore would independently require USAGE, so use a SQL primitive
  // body for the unused-list witness; language pair still must be genuine.
  q:="CREATE FUNCTION tr_unused_acl() RETURNS integer LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS 'return 1'"
  if !b.PG {
   // Native has a real no-input hstore Program, but that output itself needs
   // type USAGE. Use primitive supplied program registered by the helper.
   q="CREATE FUNCTION tr_unused_acl() RETURNS integer LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS 'one-v1'"
  }
  hostExec(t,p,b,c,q)
  transformsAssociation(t,p,b,c,"tr_unused_acl",1)
  transformsDiagnostic(t,p,c,"CREATE FUNCTION tr_type_acl(v hstore) RETURNS hstore LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS 'return v'","42501","permission denied for type hstore")
  hostExec(t,p,b,c,"RESET ROLE")
  hostExec(t,p,b,c,"GRANT USAGE ON TYPE hstore TO tr_reader")
  hostExec(t,p,b,c,"SET ROLE tr_reader")
  hostExec(t,p,b,c,"CREATE FUNCTION tr_callback_acl(v hstore) RETURNS hstore LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"increment-v1",hostIncrementPython)))
  hostBag(t,p,b,c,"tr_callback_acl('a=>7,b=>NULL'::hstore)",transformsMapRows)
  transformsDiagnostic(t,p,c,"CREATE OR REPLACE FUNCTION tr_root_owned(v hstore) RETURNS hstore LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"increment-v1",hostIncrementPython)),"42501","must be owner of function tr_root_owned")
  transformsDiagnostic(t,p,c,"CREATE OR REPLACE FUNCTION tr_callback_acl(v hstore) RETURNS hstore LANGUAGE "+hostRuntimeLanguage(b)+" TRANSFORM FOR TYPE hstore AS 'return v'","42501","permission denied for language "+hostRuntimeLanguage(b))
  hostExec(t,p,b,c,"RESET ROLE")
  hostExec(t,p,b,c,"REVOKE CREATE ON SCHEMA public FROM PUBLIC,tr_reader")
  hostExec(t,p,b,c,"SET ROLE tr_reader")
  transformsDiagnostic(t,p,c,"CREATE FUNCTION tr_schema_acl(v hstore) RETURNS hstore LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS 'return v'","42501","permission denied for schema public")
  hostExec(t,p,b,c,"RESET ROLE")
  hostExec(t,p,b,c,"GRANT CREATE ON SCHEMA public TO tr_reader")
  hostExec(t,p,b,c,"REVOKE USAGE ON LANGUAGE tr_trusted FROM PUBLIC,tr_reader")
  hostExec(t,p,b,c,"SET ROLE tr_reader")
  transformsDiagnostic(t,p,c,"CREATE FUNCTION tr_language_acl(v hstore) RETURNS hstore LANGUAGE tr_trusted TRANSFORM FOR TYPE hstore AS 'return v'","42501","permission denied for language tr_trusted")
  hostExec(t,p,b,c,"RESET ROLE")
 })
}
