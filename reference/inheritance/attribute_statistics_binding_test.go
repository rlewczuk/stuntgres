package reference_test

import (
 "fmt"
 "strings"
 "stuntgres/reference/fixture"
)

func attributeStatsBindingFailures(s attributeStatsScenario){
 s.t.Helper()
 for _,tc:=range []struct{q,message string}{
  {"SELECT pg_restore_attribute_stats()","function pg_restore_attribute_stats() does not exist"},
  {"SELECT pg_clear_attribute_stats('public','ast_t','a')","function pg_clear_attribute_stats(unknown, unknown, unknown) does not exist"},
  {"SELECT pg_clear_attribute_stats('public','ast_t','a',false,1)","function pg_clear_attribute_stats(unknown, unknown, unknown, boolean, integer) does not exist"},
  {"SELECT pg_clear_attribute_stats('public','ast_t','a',1)","function pg_clear_attribute_stats(unknown, unknown, unknown, integer) does not exist"},
  {"SELECT pg_clear_attribute_stats(bogus=>'public',relname=>'ast_t',attname=>'a',inherited=>false)","function pg_clear_attribute_stats(bogus => unknown, relname => unknown, attname => unknown, inherited => boolean) does not exist"},
  {"SELECT pg_restore_attribute_stats(VARIADIC bogus => '{}'::text[])","function pg_restore_attribute_stats(bogus => text[]) does not exist"},
  {"SELECT pg_restore_attribute_stats(kwargs => '{}'::text[])","function pg_restore_attribute_stats(kwargs => text[]) does not exist"},
 }{
  d:=fixture.ControlsReferenceDiagnostic("42883",tc.message);d.Position="8";d.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
  s.run(fixture.ImplicitError(tc.q,d))
 }
 d:=fixture.ControlsReferenceDiagnostic("XX000","variadic arguments must be name/value pairs");d.Hint="Provide an even number of variadic arguments that can be divided into pairs."
 s.run(fixture.ImplicitError("SELECT pg_restore_attribute_stats(VARIADIC NULL::text[])",d))
 d=fixture.ControlsReferenceDiagnostic("XX000","variadic arguments must be name/value pairs");d.Hint="Provide an even number of variadic arguments that can be divided into pairs."
 namedNull:=fixture.ImplicitError("SELECT pg_restore_attribute_stats(VARIADIC kwargs => NULL::text[])",d);namedNull.ErrorStage="execute";s.run(namedNull)
 namedEmpty:=fixture.ImplicitError("SELECT pg_restore_attribute_stats(VARIADIC kwargs => '{}'::text[])",fixture.ControlsReferenceDiagnostic("22023",`argument "schemaname" must not be null`));namedEmpty.ErrorStage="execute";s.run(namedEmpty)
 // A declared ANY formal does not infer the type of a bare parameter.
 unresolved:=fixture.ImplicitError("SELECT pg_restore_attribute_stats('schemaname',$1)",fixture.ControlsReferenceDiagnostic("42P18","could not determine data type of parameter $1"))
 unresolved.Args=[]any{"public"};unresolved.SelectRows=true;unresolved.ErrorStage="execute";if s.prepared{unresolved.ErrorStage="prepare"};s.run(unresolved)
 s.exec("CREATE VIEW ast_named_variadic AS SELECT pg_restore_attribute_stats(VARIADIC kwargs => '{}'::text[]) AS v")
 s.run(fixture.Select("SELECT strpos(pg_get_viewdef('ast_named_variadic'::regclass),'kwargs =>')>0 AS named",[]string{"named"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT strpos(pg_get_viewdef('ast_named_variadic'::regclass),'VARIADIC kwargs =>')>0 AS variadic_named",[]string{"variadic_named"},[]string{"BOOL"},[][]any{{true}}))
 s.exec("CREATE VIEW ast_named_clear AS SELECT pg_clear_attribute_stats(inherited=>false,attname=>'a',relname=>'ast_t',schemaname=>'public') IS NULL AS v")
 s.run(fixture.Select("SELECT strpos(pg_get_viewdef('ast_named_clear'::regclass),'inherited =>')>0 AS named",[]string{"named"},[]string{"BOOL"},[][]any{{true}}))
 d=fixture.ControlsReferenceDiagnostic("42804","VARIADIC argument must be an array");d.Position="44"
 s.run(fixture.ImplicitError("SELECT pg_restore_attribute_stats(VARIADIC 1)",d))
 // A text array cannot carry the required boolean selector; its type warning
 // precedes the required-selector error. This is not a fabricated success.
 *s.notices=nil
 s.fail("SELECT pg_restore_attribute_stats(VARIADIC '{schemaname,public,relname,ast_t,attname,a,inherited,false}'::text[])","22023",`argument "inherited" must not be null`)
 if len(*s.notices)!=1||(*s.notices)[0]!=attributeStatsWarning("01000",`argument "inherited" has type text, expected type boolean`,""){s.t.Fatalf("variadic warnings %#v",*s.notices)}
 *s.notices=nil
 s.fail("SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attnum',1::integer,'inherited',false)","22023",`must specify either "attname" or "attnum"`)
 wrongAttnum:=attributeStatsWarning("01000",`argument "attnum" has type integer, expected type smallint`,"")
 if len(*s.notices)!=1||(*s.notices)[0]!=wrongAttnum{s.t.Fatalf("wrong attnum warnings %#v",*s.notices)}
 *s.notices=nil;s.restore("'attnum',1::integer,'null_frac',.125::real",false)
 if len(*s.notices)!=1||(*s.notices)[0]!=wrongAttnum{s.t.Fatalf("partial wrong attnum warnings %#v",*s.notices)}
 s.scalars([]any{"a",false,float64(.125),int64(0),float64(0)});s.clear("a",false)
 tooMany:=make([]string,101);for i:=range tooMany{tooMany[i]=fmt.Sprint(i)}
 d=fixture.ControlsReferenceDiagnostic("54023","cannot pass more than 100 arguments to a function");d.Position="8"
 s.run(fixture.ImplicitError("SELECT pg_restore_attribute_stats("+strings.Join(tooMany,",")+")",d))
}
