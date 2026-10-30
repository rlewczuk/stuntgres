package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionACL(t *testing.T) {
 for _,tc:=range []struct{name,revoke,grant,message string}{
  {"allowed_without_language_ownership","","",""},
  {"type_usage","REVOKE USAGE ON TYPE hstore FROM PUBLIC,td_actor","GRANT USAGE ON TYPE hstore TO td_actor","permission denied for type hstore"},
  {"language_usage","REVOKE USAGE ON LANGUAGE td_lang FROM PUBLIC,td_actor","GRANT USAGE ON LANGUAGE td_lang TO td_actor","permission denied for language td_lang"},
  {"from_execute","REVOKE EXECUTE ON FUNCTION td_a_from(internal) FROM PUBLIC,td_actor","GRANT EXECUTE ON FUNCTION td_a_from(internal) TO td_actor","permission denied for function td_a_from"},
  {"to_execute","REVOKE EXECUTE ON FUNCTION td_b_to(internal) FROM PUBLIC,td_actor","GRANT EXECUTE ON FUNCTION td_b_to(internal) TO td_actor","permission denied for function td_b_to"},
 } {
  t.Run(tc.name,func(t *testing.T) {
   fixture.RoleReferenceCleanup(t,[]string{"td_actor"})
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    hostExec(t,p,b,c,"CREATE ROLE td_actor NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
    hostExec(t,p,b,c,"GRANT USAGE,CREATE ON SCHEMA public TO td_actor")
    // A genuinely declared trusted alias lets language ACLs be independently
    // revoked/granted; no security assumption about PL/Python is exercised.
    handler:="go_host_call_handler"; if b.PG { handler="plpython3_call_handler" }
    hostExec(t,p,b,c,"CREATE OR REPLACE TRUSTED LANGUAGE td_lang HANDLER "+handler)
    hostExec(t,p,b,c,"ALTER TYPE hstore OWNER TO td_actor")
    hostExec(t,p,b,c,"ALTER FUNCTION td_a_from(internal) OWNER TO td_actor")
    hostExec(t,p,b,c,"ALTER FUNCTION td_b_to(internal) OWNER TO td_actor")
    hostRows(t,p,b,c,"SELECT l.lanowner<>r.oid AS separate, r.rolsuper AS super,r.rolinherit AS inherit FROM pg_language l JOIN pg_roles r ON r.rolname='td_actor' WHERE l.lanname='td_lang'",[]string{"separate","super","inherit"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,false,false}})
    // LANGUAGE replacement must not resurrect the deliberately dropped alias
    // pair during materialization. Definition absence remains part of setup.
    definitionAbsent(t,p,b,c)
    if tc.revoke!="" { hostExec(t,p,b,c,tc.revoke) }
    hostExec(t,p,b,c,"SET ROLE td_actor")
    if tc.message!="" {
     definitionDiagnostic(t,p,c,definitionCreate("td_a_from","td_b_to",false),fixture.ControlsReferenceDiagnostic("42501",tc.message),"execute")
     hostExec(t,p,b,c,"RESET ROLE")
     definitionAbsent(t,p,b,c)
     hostExec(t,p,b,c,tc.grant)
     hostExec(t,p,b,c,"SET ROLE td_actor")
    }
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
    hostExec(t,p,b,c,"RESET ROLE")
    oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
    definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
   })
  })
 }
}

func TestReferenceRoutineTransformDefinitionLanguageDefaults(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"td_actor"})
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  hostExec(t,p,b,c,"CREATE ROLE td_actor NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
  hostExec(t,p,b,c,"GRANT USAGE,CREATE ON SCHEMA public TO td_actor")
  inquiry:=func(trusted,explicit,usage,grantable bool) {
   hostRows(t,p,b,c,"SELECT lanpltrusted AS trusted,lanacl IS NOT NULL AS explicit,has_language_privilege('td_actor',oid,'USAGE') AS usage,has_language_privilege('td_actor',oid,'USAGE WITH GRANT OPTION') AS grantable FROM pg_language WHERE lanname='td_lang'",[]string{"trusted","explicit","usage","grantable"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{trusted,explicit,usage,grantable}})
  }
  // ACL default USAGE is not permission to create untrusted-language bodies.
  inquiry(false,false,true,false)
  hostExec(t,p,b,c,"SET ROLE td_actor")
  definitionDiagnostic(t,p,c,"CREATE FUNCTION td_untrusted_probe() RETURNS integer LANGUAGE td_lang AS 'unused'",fixture.ControlsReferenceDiagnostic("42501","permission denied for language td_lang"),"execute")
  hostExec(t,p,b,c,"RESET ROLE")
  denied:=fixture.ControlsReferenceDiagnostic("42809","language \"td_lang\" is not trusted")
  denied.Detail="GRANT and REVOKE are not allowed on untrusted languages, because only superusers can use untrusted languages."
  for _,q:=range []string{"GRANT USAGE ON LANGUAGE td_lang TO td_actor","REVOKE USAGE ON LANGUAGE td_lang FROM PUBLIC"} {
   definitionDiagnostic(t,p,c,q,denied,"execute")
   inquiry(false,false,true,false)
  }
  handler:="go_host_call_handler";if b.PG {handler="plpython3_call_handler"}
  hostExec(t,p,b,c,"CREATE OR REPLACE TRUSTED LANGUAGE td_lang HANDLER "+handler)
  inquiry(true,false,true,false)
  hostExec(t,p,b,c,"REVOKE USAGE ON LANGUAGE td_lang FROM PUBLIC")
  inquiry(true,true,false,false)
  hostExec(t,p,b,c,"GRANT USAGE ON LANGUAGE td_lang TO td_actor WITH GRANT OPTION")
  inquiry(true,true,true,true)
  hostExec(t,p,b,c,"REVOKE GRANT OPTION FOR USAGE ON LANGUAGE td_lang FROM td_actor")
  inquiry(true,true,true,false)
  hostExec(t,p,b,c,"REVOKE USAGE ON LANGUAGE td_lang FROM td_actor")
  inquiry(true,true,false,false)
  // An explicit empty ACL must not revive either trusted or untrusted defaults.
  hostExec(t,p,b,c,"CREATE OR REPLACE LANGUAGE td_lang HANDLER "+handler)
  inquiry(false,true,false,false)
  definitionAbsent(t,p,b,c)
 })
}
