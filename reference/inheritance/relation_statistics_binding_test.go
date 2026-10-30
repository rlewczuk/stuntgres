package reference_test

import (
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRelationStatisticsBindingDiagnostics(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a int)")
 for _,tc:=range []struct{q,code,message string}{
  {"SELECT pg_restore_relation_stats('relpages',NULL)","22023",`argument "schemaname" must not be null`},
  {"SELECT pg_restore_relation_stats('schemaname','public')","22023",`argument "relname" must not be null`},
  {"SELECT pg_restore_relation_stats('schemaname',NULL,'relname','rst_t')","22023",`argument "schemaname" must not be null`},
  {"SELECT pg_restore_relation_stats('schemaname','public','relname',NULL)","22023",`argument "relname" must not be null`},
  {"SELECT pg_clear_relation_stats(NULL,'rst_t')","22023",`argument "schemaname" must not be null`},
  {"SELECT pg_clear_relation_stats('public',NULL)","22023",`argument "relname" must not be null`},
  {"SELECT pg_restore_relation_stats(NULL,'public')","XX000","name at variadic position 1 is null"},
  {"SELECT pg_restore_relation_stats(1,'public')","XX000","name at variadic position 1 has type integer, expected type text"},
  {"SELECT pg_restore_relation_stats(VARIADIC '{}'::text[])","22023",`argument "schemaname" must not be null`},
 }{s.fail(tc.q,tc.code,tc.message)}
 for _,q:=range []string{"SELECT pg_restore_relation_stats('schemaname')","SELECT pg_restore_relation_stats(VARIADIC NULL::text[])","SELECT pg_restore_relation_stats(VARIADIC kwargs => NULL::text[])"}{
  d:=fixture.ControlsReferenceDiagnostic("XX000","variadic arguments must be name/value pairs");d.Hint="Provide an even number of variadic arguments that can be divided into pairs."
  s.run(fixture.ImplicitError(q,d))
 }
 *s.notices=nil;s.fail("SELECT pg_restore_relation_stats('schemaname',1,'relname','rst_t')","22023",`argument "schemaname" must not be null`)
 if len(*s.notices)!=1||(*s.notices)[0]!=relationStatsWarning("01000",`argument "schemaname" has type integer, expected type text`){s.t.Fatal(*s.notices)}
 for _,tc:=range []struct{q,message string}{
  {"SELECT pg_restore_relation_stats()","function pg_restore_relation_stats() does not exist"},
  {"SELECT pg_clear_relation_stats('public')","function pg_clear_relation_stats(unknown) does not exist"},
  {"SELECT pg_clear_relation_stats('public','rst_t',1)","function pg_clear_relation_stats(unknown, unknown, integer) does not exist"},
  {"SELECT pg_clear_relation_stats('public',1)","function pg_clear_relation_stats(unknown, integer) does not exist"},
  {"SELECT pg_clear_relation_stats(bogus=>'public',relname=>'rst_t')","function pg_clear_relation_stats(bogus => unknown, relname => unknown) does not exist"},
  {"SELECT pg_restore_relation_stats(kwargs=>'{}'::text[])","function pg_restore_relation_stats(kwargs => text[]) does not exist"},
  {"SELECT pg_restore_relation_stats(VARIADIC bogus=>'{}'::text[])","function pg_restore_relation_stats(bogus => text[]) does not exist"},
 }{
  d:=fixture.ControlsReferenceDiagnostic("42883",tc.message);d.Position="8";d.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
  s.run(fixture.ImplicitError(tc.q,d))
 }
 d:=fixture.ControlsReferenceDiagnostic("42804","VARIADIC argument must be an array");d.Position="43"
 s.run(fixture.ImplicitError("SELECT pg_restore_relation_stats(VARIADIC 1)",d))
 d=fixture.ControlsReferenceDiagnostic("42883","function pg_clear_relation_stats(integer) does not exist");d.Position="19";d.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
 s.run(fixture.ImplicitError("SELECT 'é'::text, pg_clear_relation_stats(1)",d))
 many:=make([]string,101);for i:=range many{many[i]=fmt.Sprint(i)}
 d=fixture.ControlsReferenceDiagnostic("54023","cannot pass more than 100 arguments to a function");d.Position="8"
 s.run(fixture.ImplicitError("SELECT pg_restore_relation_stats("+strings.Join(many,",")+")",d))
 unresolved:=fixture.ImplicitError("SELECT pg_restore_relation_stats('schemaname',$1)",fixture.ControlsReferenceDiagnostic("42P18","could not determine data type of parameter $1"))
 unresolved.Args=[]any{"public"};unresolved.SelectRows=true;unresolved.ErrorStage="execute";if s.prepared{unresolved.ErrorStage="prepare"};s.run(unresolved)
 s.restore("'relpages',NULL,'reltuples',NULL,'relallvisible',NULL,'relallfrozen',NULL",true)
 s.run(fixture.Select("SELECT pg_restore_relation_stats(VARIADIC kwargs => '{schemaname,public,relname,rst_t}'::text[])",[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT pg_clear_relation_stats(relname=>'rst_t',schemaname=>'public')",[]string{"pg_clear_relation_stats"},[]string{"VOID"},[][]any{{""}}))
 s.stats(0,-1,0,0)
})}
