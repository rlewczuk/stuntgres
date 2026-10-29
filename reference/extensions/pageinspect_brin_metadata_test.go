package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

var brinMetaNames=[]string{"magic","version","pagesperrange","lastrevmappage"}
var brinMetaTypes=[]string{"TEXT","INT4","INT4","INT8"}

func TestReferencePageinspectBrinMetadata(t *testing.T){
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{"CREATE TABLE pi_data(n integer)","CREATE INDEX pi_idx ON pi_data USING brin(n)"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  classifier:="SELECT brin_part.brin_page_type($1)"
  meta:="SELECT * FROM brin_part.brin_metapage_info($1)"
  scalarMeta:="SELECT brin_part.brin_metapage_info($1)"
  revmap:="SELECT * FROM brin_part.brin_revmap_data($1)"
  items:="SELECT * FROM brin_part.brin_page_items($1,'pi_idx'::regclass) ORDER BY itemoffset,attnum"
  revRows:=make([][]any,1360)
  for i:=range revRows{revRows[i]=[]any{"(0,0)"}}
  families:=[]struct{name,empty string;cases []fixture.Step}{
   {"classifier",classifier+" WHERE false",[]fixture.Step{
    fixture.Select(classifier,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{"meta"}},fixture.BRINMeta()),
    fixture.Select(classifier,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{nil}},make([]byte,8192)),
    fixture.Select(classifier,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{nil}},nil),
   }},
   {"metapage",meta+" WHERE false",[]fixture.Step{
    fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{"0xA8109CFA",int64(1),int64(128),int64(1)}},fixture.BRINMeta()),
    fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{nil,nil,nil,nil}},make([]byte,8192)),
    fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{nil,nil,nil,nil}},nil),
   }},
   {"metapage_scalar",scalarMeta+" WHERE false",[]fixture.Step{
    fixture.Select(scalarMeta,[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{"(0xA8109CFA,1,128,1)"}},fixture.BRINMeta()),
    fixture.Select(scalarMeta,[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{nil}},make([]byte,8192)),
    fixture.Select(scalarMeta,[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{nil}},nil),
   }},
   {"revmap",revmap+" WHERE false",[]fixture.Step{
    fixture.Select(revmap,[]string{"pages"},[]string{"TID"},revRows,fixture.BRINImage(0xf092)),
    fixture.Select(revmap,[]string{"pages"},[]string{"TID"},[][]any{{nil}},make([]byte,8192)),
    fixture.Select(revmap,[]string{"pages"},[]string{"TID"},nil,nil),
   }},
   {"items","SELECT * FROM brin_part.brin_page_items($1,'pi_idx'::regclass) WHERE false",[]fixture.Step{
    brinItemStep(brinItems(brinInt4(-7,42)),[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,"{-7 .. 42}"}}),
    brinItemStep(make([]byte,8192),nil),
    fixture.Select(items,brinItemNames,brinItemTypes,nil,nil),
    brinItemStep(brinItems(nil),[][]any{{int64(1),nil,nil,nil,nil,nil,nil,nil}}),
    brinItemStep(brinItems(brinTuple(0x80,[]byte{1},nil)),[][]any{{int64(1),int64(2147549186),int64(1),true,false,false,false,nil}}),
   }},
  }
  for _,family:=range families{t.Run(family.name,func(t *testing.T){
   q:=family.cases[0].Query
   statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer statement.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
   for _,s:=range family.cases{
    s=fixture.BRINMetadata(s,b.PG,false);s.Bag=true
    fixture.BRINBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   }
   empty:=family.cases[0];empty.Query=family.empty;empty.Rows=nil
   fixture.BRINBoth(t,p,b,c,fixture.BRINMetadata(empty,b.PG,false))
  })}
 })
}
