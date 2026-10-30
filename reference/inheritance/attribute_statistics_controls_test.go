package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceAttributeStatisticsNativeControls(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 s.exec("CREATE DOMAIN ast_domain AS integer")
 s.exec("CREATE TABLE ast_t(a ast_domain,b INT,arr INT[],r int4range)")
 s.restore("'null_frac',.25::real,'n_distinct',7::real,'most_common_vals','{1,2}','most_common_freqs','{0.5,0.25}'::real[],'histogram_bounds','{3,5,9}','correlation',.75::real",true)
 s.run(fixture.Select(attributeStatsRestore("a",true,"'null_frac',0::real,'n_distinct',0::real"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select(attributeStatsRestore("arr",false,"'most_common_elems','{1,2}','most_common_elem_freqs','{0.5,0.25,0.25,0.5,0}'::real[],'elem_count_histogram','{0,1,2,3}'::real[]"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select(attributeStatsRestore("arr",false,`'most_common_vals','{"{1,2}","{3}"}','most_common_freqs','{0.5,0.25}'::real[]`),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select(attributeStatsRestore("r",false,`'range_length_histogram','{1,3,9}','range_empty_frac',.125::real,'range_bounds_histogram','{"[1,2)","[5,9)"}'`),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Exec("INSERT INTO ast_t VALUES(99,9,'{9}','[90,99)')",1))
 s.run(fixture.Select(attributeStatsRestore("b",false,"'null_frac',.5::real"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}));s.clear("b",false)
 q:="SELECT attname,inherited,null_frac,n_distinct,most_common_vals::text,most_common_elems::text,range_length_histogram::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' ORDER BY attname,inherited"
 want:=[][]any{{"a",false,float64(.25),float64(7),"{1,2}",nil,nil},{"a",true,float64(0),float64(0),nil,nil,nil},{"arr",false,float64(0),float64(0),`{"{1,2}","{3}"}`,"{1,2}",nil},{"r",false,float64(0),float64(0),nil,nil,"{1,3,9}"}}
 step:=fixture.Select(q,[]string{"attname","inherited","null_frac","n_distinct","most_common_vals","most_common_elems","range_length_histogram"},[]string{"NAME","BOOL","FLOAT4","FLOAT4","TEXT","TEXT","TEXT"},want)
 s.run(step)
 if s.b.PG{return}
 s.exec(`CREATE TABLE ast_snapshot_targets(j json[],v varchar(3) COLLATE "C")`)
 s.run(fixture.Select("SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_snapshot_targets','attname','j','inherited',false,'null_frac',.125::real)",[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT pg_restore_attribute_stats('schemaname','public','relname','ast_snapshot_targets','attname','v','inherited',false,'most_common_vals','{ab}','most_common_freqs','{1}'::real[])",[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 var saved bytes.Buffer;if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&saved);err!=nil{s.t.Fatal(err)}
 driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector(s.t.Name()+"/independent");if err!=nil{s.t.Fatal(err)};other:=sql.OpenDB(connector);defer other.Close()
 if _,err=other.ExecContext(s.p.Ctx,"CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(7)");err!=nil{s.t.Fatal(err)}
 if _,err=other.ExecContext(s.p.Ctx,"SELECT pg_restore_attribute_stats('schemaname','public','relname','sentinel','attname','v','inherited',false,'null_frac',.75::real,'n_distinct',11::real)");err!=nil{s.t.Fatal(err)}
 badScripts:=[]string{saved.String()+"\n-- stuntgres-attribute-statistics-v2 e30=\n",saved.String()+"\n-- stuntgres-attribute-statistics-v1 !!!\n",saved.String()+"\nINVALID SQL"}
 badScripts=append(badScripts,attributeStatsMalformedSnapshots(s,saved.String())...)
 for i,bad:=range badScripts{
  if err=stuntgres.Restore(s.p.Ctx,other,strings.NewReader(bad));err==nil{s.t.Fatal("malformed staged snapshot accepted",i)}
  var v int;if err=other.QueryRowContext(s.p.Ctx,"SELECT v FROM sentinel").Scan(&v);err!=nil||v!=7{s.t.Fatal(v,err)}
  var fraction,distinct float64
  if err=other.QueryRowContext(s.p.Ctx,"SELECT null_frac,n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='sentinel' AND attname='v' AND NOT inherited").Scan(&fraction,&distinct);err!=nil||fraction!=.75||distinct!=11{s.t.Fatal("failed semantic import changed destination statistics",fraction,distinct,err)}
 }
 canceled,cancel:=context.WithCancel(s.p.Ctx);cancel();if err=stuntgres.Restore(canceled,other,bytes.NewReader(saved.Bytes()));!errors.Is(err,context.Canceled){s.t.Fatal(err)}
 if err=stuntgres.Restore(s.p.Ctx,other,bytes.NewReader(saved.Bytes()));err!=nil{s.t.Fatal(err)}
 rows,err:=other.QueryContext(s.p.Ctx,q);if err!=nil{s.t.Fatal(err)};defer rows.Close();var got [][]any
 for rows.Next(){values:=make([]any,7);dest:=make([]any,7);for i:=range values{dest[i]=&values[i]};if err=rows.Scan(dest...);err!=nil{s.t.Fatal(err)};for i,v:=range values{if b,ok:=v.([]byte);ok{values[i]=string(b)}};got=append(got,values)};if err=rows.Err();err!=nil{s.t.Fatal(err)}
 if _,ok:=fixture.CompareRows(got,want,false);!ok{s.t.Fatalf("independent imported state %#v want %#v",got,want)}
 var again bytes.Buffer;if err=stuntgres.Dump(s.p.Ctx,other,&again);err!=nil{s.t.Fatal(err)};if !bytes.Equal(saved.Bytes(),again.Bytes()){s.t.Fatal("semantic redump changed")}
 attributeStatsNativeLifetimes(s)
})}
