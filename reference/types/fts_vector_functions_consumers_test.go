package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceFTSVectorFunctionsConsumers(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
  run(fixture.Exec(`CREATE TABLE fts_vf_saved(id integer,v tsvector DEFAULT setweight('cat:1B rat:2C'::tsvector,'A'),a text[],w "char"[])`,0))
  run(fixture.Exec("INSERT INTO fts_vf_saved(id,a,w) VALUES(1,tsvector_to_array('cat rat'::tsvector),'{A,B,NULL}'),(2,'{}','{}')",2))
  run(fixture.Exec(`CREATE VIEW fts_vf_view AS SELECT id,length(v) AS n,strip(v) AS s,ts_delete(v,'rat') AS d,ts_filter(v,'{A}') AS f,tsvector_to_array(v) AS a FROM fts_vf_saved`,0))
  checks:=[]fixture.Step{
   fixture.Select("SELECT id,v,a,w FROM fts_vf_saved ORDER BY id",[]string{"id","v","a","w"},[]string{"INT4","TSVECTOR","_TEXT","_CHAR"},[][]any{{int64(1),"'cat':1A 'rat':2A","{cat,rat}","{A,B,NULL}"},{int64(2),"'cat':1A 'rat':2A","{}","{}"}}),
   fixture.Select("SELECT id,n,s,d,f,a FROM fts_vf_view ORDER BY id",[]string{"id","n","s","d","f","a"},[]string{"INT4","INT4","TSVECTOR","TSVECTOR","TSVECTOR","_TEXT"},[][]any{{int64(1),int64(2),"'cat' 'rat'","'cat':1A","'cat':1A 'rat':2A","{cat,rat}"},{int64(2),int64(2),"'cat' 'rat'","'cat':1A","'cat':1A 'rat':2A","{cat,rat}"}}),
  }
  // The shared control cycle requires an explicit raw metadata assertion before
  // retaining the cursor; ordinary fixture steps still assert all type names.
  for i:=range checks {want:=checks[i].Types;checks[i].CheckMetadata=func(t *testing.T,rows *sql.Rows){columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};if len(columns)!=len(want){t.Fatal("metadata width")};for j,col:=range columns{if col.DatabaseTypeName()!=want[j]{t.Fatal("metadata type",j,col.DatabaseTypeName(),want[j])}}}}
  for _,s:=range checks{run(s);run(fixture.Prepared(s))}
  if !b.PG{
   failures:=[]rangeStagedFailure{{
    script:`CREATE TABLE fts_vf_staged(id integer,source tsvector,w text,v tsvector GENERATED ALWAYS AS (setweight(source,w::"char")) STORED);
INSERT INTO fts_vf_staged(id,source,w) VALUES(1,'cat:1B rat:2C','A');
INSERT INTO fts_vf_staged(id,source,w) VALUES(2,'cat:1B rat:2C','Z');`,
    diagnostic:fixture.ControlsReferenceDiagnostic("22023",`unrecognized weight: "Z"`),
   }}
   rangeControlCycle(t,p,b,checks,[][]bool{{true,true,true,true},{true,false,false,false,false,false}},failures)
  }
  run(fixture.Exec("INSERT INTO fts_vf_saved(id) VALUES(3)",1))
  run(fixture.Select("SELECT n,s,d,f,a FROM fts_vf_view WHERE id=3",[]string{"n","s","d","f","a"},[]string{"INT4","TSVECTOR","TSVECTOR","TSVECTOR","_TEXT"},[][]any{{int64(2),"'cat' 'rat'","'cat':1A","'cat':1A 'rat':2A","{cat,rat}"}}))
 })
}
func TestReferenceFTSVectorFunctionsAtomicityCancellation(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
  run(fixture.Exec("CREATE TABLE fts_vf_atomic(id integer,v tsvector)",0));run(fixture.Exec("INSERT INTO fts_vf_atomic VALUES(1,'sentinel:1B')",1))
  query:=`INSERT INTO fts_vf_atomic VALUES(2,setweight('a:1'::tsvector,'A')),(3,setweight('b:2'::tsvector,$1::"char"))`
  d:=fixture.ControlsReferenceDiagnostic("22023",`unrecognized weight: "Z"`)
  for _,prepared:=range []bool{false,true}{s:=fixture.ErrorDiagnostic(query,d,"Z");s.Prepared=prepared;run(s);run(fixture.Select("SELECT id,v FROM fts_vf_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","TSVECTOR"},[][]any{{int64(1),"'sentinel':1B"}}))}
  if !b.PG{ctx,cancel:=context.WithCancel(p.Ctx);cancel();for _,q:=range []string{`SELECT ts_filter('a:1'::tsvector,'{A}')`,query}{if _,err:=b.DB.ExecContext(ctx,q,"A");!errors.Is(err,context.Canceled){t.Fatal("pre-canceled vector operation",err)}}}
  run(fixture.Exec(query,2,"D"));run(fixture.Select("SELECT id,v FROM fts_vf_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","TSVECTOR"},[][]any{{int64(1),"'sentinel':1B"},{int64(2),"'a':1A"},{int64(3),"'b':2"}}))
 })
}
