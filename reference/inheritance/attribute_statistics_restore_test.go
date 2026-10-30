package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceAttributeStatisticsRestoreClear(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 s.exec("CREATE TABLE ast_t(a INT,b INT)")
 s.restore("'null_frac',.25::real,'avg_width',4::integer,'n_distinct',7::real",true)
 s.scalars([]any{"a",false,float64(.25),int64(4),float64(7)})
 s.run(fixture.Select(attributeStatsRestore("a",true,"'null_frac',.5::real"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select(attributeStatsRestore("b",false,"'n_distinct',3::real"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attnum',1::smallint,'inherited',false,'avg_width',8::integer)",[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.restore("'NULL_FRAC',.5::real,'null_frac',NULL,'version','ignored'::text,'unrecognized',NULL",true)
 s.scalars([]any{"a",false,float64(.5),int64(8),float64(7)},[]any{"a",true,float64(.5),int64(0),float64(0)},[]any{"b",false,float64(0),int64(0),float64(3)})
 s.clear("a",false);s.clear("a",false)
 s.scalars([]any{"a",true,float64(.5),int64(0),float64(0)},[]any{"b",false,float64(0),int64(0),float64(3)})
 s.run(fixture.Select("SELECT pg_clear_attribute_stats('public','ast_t','a',false) IS NULL AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}))
})}

func TestReferenceAttributeStatisticsBinding(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 s.exec("CREATE TABLE ast_t(a INT,b INT)")
 q:="SELECT pg_catalog.pg_restore_attribute_stats('schemaname',$1::text,'relname',$2::text,'attname',$3::text,'inherited',$4::boolean,'null_frac',$5::real) AS restored"
 held,err:=s.conn.PrepareContext(s.p.Ctx,q);if err!=nil{t.Fatal(err)};defer held.Close()
 s.scalars()
 for _,fraction:=range []float64{.25,.5}{fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{s.conn,held,q},s.b.Recorder,fixture.Select(q,[]string{"restored"},[]string{"BOOL"},[][]any{{true}},"public","ast_t","a",false,fraction));s.scalars([]any{"a",false,fraction,int64(0),float64(0)})}
 s.run(fixture.Select(attributeStatsRestore("a",false,"'null_frac',.75::real")+" LIMIT 0",[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},nil))
 s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)})
 s.run(fixture.Select("SELECT pg_clear_attribute_stats(inherited=>false,attname=>'a',relname=>'ast_t',schemaname=>'public')",[]string{"pg_clear_attribute_stats"},[]string{"VOID"},[][]any{{""}}))
 s.scalars()
 for _,tc:=range []struct{q,code,message string}{
  {"SELECT pg_restore_attribute_stats('schemaname')","XX000","variadic arguments must be name/value pairs"},
  {"SELECT pg_restore_attribute_stats(NULL,'public')","XX000","name at variadic position 1 is null"},
  {"SELECT pg_restore_attribute_stats('version',1,NULL,'public')","XX000","name at variadic position 3 is null"},
  {"SELECT pg_restore_attribute_stats(1,'public')","XX000","name at variadic position 1 has type integer, expected type text"},
  {"SELECT pg_restore_attribute_stats('version',1)","22023",`argument "schemaname" must not be null`},
  {"SELECT pg_restore_attribute_stats('schemaname','public')","22023",`argument "relname" must not be null`},
  {"SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t')","22023",`must specify either "attname" or "attnum"`},
  {"SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attname','a')","22023",`argument "inherited" must not be null`},
  {"SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attname','a','attnum',1::smallint,'inherited',false)","22023",`cannot specify both "attname" and "attnum"`},
  {"SELECT pg_restore_attribute_stats(VARIADIC '{}'::text[])","22023",`argument "schemaname" must not be null`},
 }{if tc.message=="variadic arguments must be name/value pairs"{d:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);d.Hint="Provide an even number of variadic arguments that can be divided into pairs.";s.run(fixture.ImplicitError(tc.q,d))}else{s.fail(tc.q,tc.code,tc.message)}}
 for _,tc:=range []struct{args,key string}{{"NULL,'ast_t','a',false","schemaname"},{"'public',NULL,'a',false","relname"},{"'public','ast_t',NULL,false","attname"},{"'public','ast_t','a',NULL","inherited"}}{s.fail("SELECT pg_clear_attribute_stats("+tc.args+")","22023",`argument "`+tc.key+`" must not be null`)}
 attributeStatsBindingFailures(s)
})}
