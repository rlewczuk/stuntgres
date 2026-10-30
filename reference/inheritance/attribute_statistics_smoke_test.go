package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceAttributeStatisticsFeatureSmoke(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 attributeStatsPopulation(s)
 // Clear measured b so the selected three-row lifecycle is exact in both modes.
 s.clear("b",false)
 s.restore("'null_frac',.25::real,'avg_width',4::integer,'n_distinct',7::real",true)
 s.scalars([]any{"a",false,float64(.25),int64(4),float64(7)})
 q:="SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_t','attname','a','inherited',false,'null_frac',$1::real)"
 held,err:=s.conn.PrepareContext(s.p.Ctx,q);if err!=nil{s.t.Fatal(err)};defer held.Close()
 fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{s.conn,held,q},s.b.Recorder,fixture.Select(q,[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}},float64(.5)))
 s.scalars([]any{"a",false,float64(.5),int64(4),float64(7)})
 s.warnings("'null_frac',.25::real,'most_common_vals','{bad}','most_common_freqs','{0.5}'::real[]",attributeStatsWarning("22P02",`invalid input syntax for type integer: "bad"`,""))
 s.restore("'most_common_vals','{1}','most_common_freqs','{0.5}'::real[]",true);s.estimate("a=1",50)
 for _,column:=range []string{"a","b"}{inherited:=column=="a";s.run(fixture.Select(attributeStatsRestore(column,inherited,"'null_frac',.75::real"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))}
 s.clear("a",false)
 s.scalars([]any{"a",true,float64(.75),int64(0),float64(0)},[]any{"b",false,float64(.75),int64(0),float64(0)})
 s.exec("BEGIN");s.restore("'null_frac',.5::real",true);s.exec("ROLLBACK")
 s.scalars([]any{"a",true,float64(.75),int64(0),float64(0)},[]any{"b",false,float64(.75),int64(0),float64(0)})
 if !s.b.PG{
  var saved bytes.Buffer;if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&saved);err!=nil{s.t.Fatal(err)}
  if err:=stuntgres.Reset(s.p.Ctx,s.p.Native);err!=nil{s.t.Fatal(err)}
  if err:=stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(saved.Bytes()));err!=nil{s.t.Fatal(err)}
  s.scalars([]any{"a",true,float64(.75),int64(0),float64(0)},[]any{"b",false,float64(.75),int64(0),float64(0)})
  driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector(s.t.Name()+"/smoke-independent");if err!=nil{s.t.Fatal(err)}
  other:=sql.OpenDB(connector);defer other.Close()
  if err=stuntgres.Restore(s.p.Ctx,other,bytes.NewReader(saved.Bytes()));err!=nil{s.t.Fatal(err)}
  recorded,recorder:=fixture.RecordedNativePool(s.t,connector)
  fixture.RunStep(s.t,s.p.Ctx,recorded,recorder,fixture.Select("SELECT attname,inherited,null_frac,avg_width,n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' ORDER BY attname,inherited",[]string{"attname","inherited","null_frac","avg_width","n_distinct"},[]string{"NAME","BOOL","FLOAT4","INT4","FLOAT4"},[][]any{{"a",true,float64(.75),int64(0),float64(0)},{"b",false,float64(.75),int64(0),float64(0)}}))
 }
})}
