package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionSyntax(t *testing.T) {
 for _,directions:=range []string{"from","to","both","reverse","bare_callback"} {
  t.Run(directions,func(t *testing.T) {
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    from,to:="td_a_from","td_b_to"
    if directions=="from" { to="" }; if directions=="to" { from="" }
    q:=definitionCreate(from,to,false)
    if directions=="reverse" { q="CREATE TRANSFORM FOR public.hstore LANGUAGE td_lang (TO SQL WITH FUNCTION public.\"td_b_to\"(pg_catalog.internal), FROM SQL WITH FUNCTION public.\"td_a_from\"(pg_catalog.internal))" }
    if directions=="bare_callback" { to=""; q="CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from)" }
    if directions=="bare_callback" {
     hostExec(t,p,b,c,q)
     definitionPair(t,p,b,c,from,to)
     hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang")
    }
    held:=transformsHeld(t,p,c,q)
    hostExec(t,p,b,held,q)
    oid:=definitionPair(t,p,b,c,from,to)
    definitionEdges(t,p,b,c,oid,from,to)
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",true))
    if next:=definitionPair(t,p,b,c,"td_a_from","td_b_to"); next!=oid { t.Errorf("replacement OID %d want %d",next,oid) }
    hostExec(t,p,b,c,"DROP TRANSFORM FOR public.hstore LANGUAGE td_lang RESTRICT")
    definitionAbsent(t,p,b,c)
    if directions=="bare_callback" {
     hostExec(t,p,b,c,"CREATE FUNCTION td_a_from(v integer) RETURNS integer LANGUAGE SQL RETURN v")
     want:=fixture.ControlsReferenceDiagnostic("42725","function name \"td_a_from\" is not unique")
     want.Hint="Specify the argument list to select the function unambiguously."
     definitionDiagnostic(t,p,c,q,want,"execute")
     definitionAbsent(t,p,b,c)
     hostExec(t,p,b,c,definitionCreate("td_a_from","",false))
     definitionPair(t,p,b,c,"td_a_from","")
     hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang")
    }
   })
  })
 }
 for _,tc:=range []struct{name,q,token string}{
  {"empty","CREATE TRANSFORM FOR hstore LANGUAGE td_lang ()",")"},
  {"repeat_from","CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from(internal), FROM SQL WITH FUNCTION td_a_from(internal))","FROM"},
  {"repeat_to","CREATE TRANSFORM FOR hstore LANGUAGE td_lang (TO SQL WITH FUNCTION td_b_to(internal), TO SQL WITH FUNCTION td_b_to(internal))","TO"},
  {"third","CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from(internal), TO SQL WITH FUNCTION td_b_to(internal), FROM SQL WITH FUNCTION td_a_from(internal))",","},
  {"trailing","CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from(internal)) trailing","trailing"},
  {"unicode","/* é */ CREATE TRANSFORM FOR \"hstore\" LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from(internal)) trailing","trailing"},
 } {
  t.Run(tc.name,func(t *testing.T) {
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionDiagnostic(t,p,c,tc.q,definitionSyntaxDiagnostic(tc.q,tc.token),"prepare")
    definitionAbsent(t,p,b,c)
   })
  })
 }
}

func TestReferenceRoutineTransformDefinitionValidation(t *testing.T) {
 for _,tc:=range []struct{name,setup,q,code,message string}{
  {name:"pseudo",q:"CREATE TRANSFORM FOR internal LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from(internal))",code:"42809",message:"data type internal is a pseudo-type"},
  {name:"domain",setup:"CREATE DOMAIN td_domain AS integer",q:"CREATE TRANSFORM FOR td_domain LANGUAGE td_lang (FROM SQL WITH FUNCTION td_a_from(internal))",code:"42809",message:"data type td_domain is a domain"},
  {name:"from_return",q:"CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION td_b_to(internal))",code:"42P17",message:"return data type of FROM SQL function must be internal"},
  {name:"to_return",q:"CREATE TRANSFORM FOR hstore LANGUAGE td_lang (TO SQL WITH FUNCTION td_a_from(internal))",code:"42P17",message:"return data type of TO SQL function must be the transform data type"},
  {name:"input_type",q:"CREATE TRANSFORM FOR hstore LANGUAGE td_lang (TO SQL WITH FUNCTION hstore_in(cstring))",code:"42P17",message:"first argument of transform function must be type internal"},
  {name:"arity",q:"CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION pg_catalog.dsimple_lexize(internal,internal,internal,internal))",code:"42P17",message:"transform function must take one argument"},
  {name:"zero_arguments",q:"CREATE TRANSFORM FOR integer LANGUAGE td_lang (TO SQL WITH FUNCTION pg_catalog.pg_backend_pid())",code:"42P17",message:"transform function must take one argument"},
  {name:"two_arguments",q:"CREATE TRANSFORM FOR integer LANGUAGE td_lang (TO SQL WITH FUNCTION pg_catalog.int4pl(integer,integer))",code:"42P17",message:"transform function must take one argument"},
  {name:"set_returning",q:"CREATE TRANSFORM FOR integer LANGUAGE td_lang (TO SQL WITH FUNCTION pg_catalog.generate_series(integer,integer))",code:"42P17",message:"transform function must not return a set"},
  {name:"aggregate",q:"CREATE TRANSFORM FOR bigint LANGUAGE td_lang (TO SQL WITH FUNCTION pg_catalog.count())",code:"42P17",message:"transform function must be a normal function"},
  {name:"window",q:"CREATE TRANSFORM FOR bigint LANGUAGE td_lang (TO SQL WITH FUNCTION pg_catalog.row_number())",code:"42P17",message:"transform function must be a normal function"},
  {name:"unknown_language",q:"CREATE TRANSFORM FOR hstore LANGUAGE td_missing (FROM SQL WITH FUNCTION td_a_from(internal))",code:"42704",message:"language \"td_missing\" does not exist"},
  {name:"unknown_callback",q:"CREATE TRANSFORM FOR hstore LANGUAGE td_lang (FROM SQL WITH FUNCTION td_missing(internal))",code:"42883",message:"function td_missing(internal) does not exist"},
 } {
  t.Run(tc.name,func(t *testing.T) {
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    if tc.setup!="" { hostExec(t,p,b,c,tc.setup) }
    definitionDiagnostic(t,p,c,tc.q,fixture.ControlsReferenceDiagnostic(tc.code,tc.message),"execute")
    definitionAbsent(t,p,b,c)
    hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_transform t JOIN pg_language l ON t.trflang=l.oid WHERE l.lanname='td_lang'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
    // Corrected identical type/language is not poisoned by failed admission.
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
    definitionPair(t,p,b,c,"td_a_from","td_b_to")
   })
  })
 }
 t.Run("duplicate_precedence",func(t *testing.T) {
  definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   definitionAnnotate(t,p,b,c)
   hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
   oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
   definitionDiagnostic(t,p,c,definitionCreate("td_b_to","",false),fixture.ControlsReferenceDiagnostic("42P17","return data type of FROM SQL function must be internal"),"execute")
   definitionDiagnostic(t,p,c,definitionCreate("td_a_from","td_b_to",false),fixture.ControlsReferenceDiagnostic("42710","transform for type hstore language \"td_lang\" already exists"),"execute")
   definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  })
 })
}

func TestReferenceRoutineTransformDefinitionDropOwnership(t *testing.T) {
 // PostgreSQL's manual requires both type and language ownership; released
 // REL_18_6 checks type ownership. These independently isolated role witnesses
 // resolve that discrepancy without superuser/membership/schema escape routes.
 for _,tc:=range []struct{name,actor string; languageOwner bool; allowed bool}{
  {"type_only","td_type_owner",false,true},
  {"language_only","td_language_owner",false,false},
  {"neither","td_outsider",false,false},
  {"both","td_type_owner",true,true},
 } {
  t.Run(tc.name,func(t *testing.T) {
   fixture.RoleReferenceCleanup(t,[]string{"td_type_owner","td_language_owner","td_outsider"})
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    for _,role:=range []string{"td_type_owner","td_language_owner","td_outsider"} { hostExec(t,p,b,c,"CREATE ROLE "+role+" NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT") }
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
    oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
    hostExec(t,p,b,c,"GRANT USAGE,CREATE ON SCHEMA public TO td_type_owner, td_language_owner, td_outsider")
    hostExec(t,p,b,c,"GRANT USAGE ON TYPE hstore TO td_type_owner, td_language_owner, td_outsider")
    hostExec(t,p,b,c,"ALTER TYPE hstore OWNER TO td_type_owner")
    owner:="td_language_owner"; if tc.languageOwner { owner="td_type_owner" }
    hostExec(t,p,b,c,"ALTER LANGUAGE td_lang OWNER TO "+owner)
    hostExec(t,p,b,c,"SET ROLE "+tc.actor)
    q:="DROP TRANSFORM FOR hstore LANGUAGE td_lang"
    if tc.allowed { hostExec(t,p,b,c,q) } else {
     definitionDiagnostic(t,p,c,q,fixture.ControlsReferenceDiagnostic("42501","must be owner of type hstore"),"execute")
    }
    hostExec(t,p,b,c,"RESET ROLE")
    if tc.allowed { definitionAbsent(t,p,b,c) } else { definitionPair(t,p,b,c,"td_a_from","td_b_to"); definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to") }
   })
  })
 }
}

func TestReferenceRoutineTransformDefinitionVolatility(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  // PG C declarations were explicitly immutable on installation; set them
  // VOLATILE here to exercise the same explicit SQL promise as native defaults.
  if b.PG { hostExec(t,p,b,c,"ALTER FUNCTION td_a_from(internal) VOLATILE") }
  hostRows(t,p,b,c,"SELECT provolatile::text AS v FROM pg_proc WHERE proname='td_a_from'",[]string{"v"},[]string{"TEXT"},[][]any{{"v"}})
  definitionDiagnostic(t,p,c,definitionCreate("td_a_from","",false),fixture.ControlsReferenceDiagnostic("42P17","transform function must not be volatile"),"execute")
  definitionAbsent(t,p,b,c)
  hostExec(t,p,b,c,"ALTER FUNCTION td_a_from(internal) STABLE")
  hostExec(t,p,b,c,definitionCreate("td_a_from","",false))
  definitionPair(t,p,b,c,"td_a_from","")
  hostExec(t,p,b,c,"ALTER FUNCTION td_a_from(internal) IMMUTABLE")
  hostExec(t,p,b,c,definitionCreate("td_a_from","",true))
  // Existing references do not prohibit later volatility alteration; CREATE
  // validates the current promise, rather than perpetual provider validation.
  hostExec(t,p,b,c,"ALTER FUNCTION td_a_from(internal) VOLATILE")
  definitionDiagnostic(t,p,c,definitionCreate("td_a_from","",true),fixture.ControlsReferenceDiagnostic("42P17","transform function must not be volatile"),"execute")
  definitionPair(t,p,b,c,"td_a_from","")
 })
}
