package reference_test

import (
 "database/sql"
 "reflect"
 "testing"

 "stuntgres/reference/fixture"
)

// Scan types follow the public adapters, not the values in the first row.
// pq v1.12.3 rows.go fieldDesc.Type reports TID as interface{} and INT4 as
// int32; native reports immutable TID text and its common int64 integer carrier.
// Function expressions/sources have unknown native nullability; stored view
// columns are declared nullable. pq honestly reports unknown nullability.
func brinMetadata(s fixture.Step,pg,view bool)fixture.Step{
 names,types:=s.Columns,s.Types
 s.CheckMetadata=func(t *testing.T,rows *sql.Rows){
  t.Helper()
  columns,err:=rows.ColumnTypes()
  if err!=nil || len(columns)!=len(names){t.Fatal("BRIN metadata width",columns,err)}
  for i,column:=range columns{
   if column.Name()!=names[i] || column.DatabaseTypeName()!=types[i]{t.Fatal("BRIN column declaration",i,column.Name(),column.DatabaseTypeName(),names[i],types[i])}
   var scan reflect.Type
   switch types[i]{
   case "TEXT":scan=reflect.TypeFor[string]()
   case "BOOL":scan=reflect.TypeFor[bool]()
   case "INT4":scan=reflect.TypeFor[int64]();if pg{scan=reflect.TypeFor[int32]()}
   case "INT8":scan=reflect.TypeFor[int64]()
   case "TID","RECORD":scan=reflect.TypeFor[string]();if pg{scan=reflect.TypeFor[any]()}
   default:t.Fatal("missing independent BRIN scan expectation",types[i])
   }
   if column.ScanType()!=scan{t.Fatal("BRIN scan type",i,column.ScanType(),scan)}
   nullable,known:=column.Nullable()
   want:=view && !pg
   if nullable!=want || known!=want{t.Fatal("BRIN nullable declaration",i,nullable,known,want,want)}
  }
 }
 return s
}

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
    fixture.Select(classifier,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{"meta"}},brinMeta()),
    fixture.Select(classifier,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{nil}},make([]byte,8192)),
    fixture.Select(classifier,[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{nil}},nil),
   }},
   {"metapage",meta+" WHERE false",[]fixture.Step{
    fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{"0xA8109CFA",int64(1),int64(128),int64(1)}},brinMeta()),
    fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{nil,nil,nil,nil}},make([]byte,8192)),
    fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{nil,nil,nil,nil}},nil),
   }},
   {"metapage_scalar",scalarMeta+" WHERE false",[]fixture.Step{
    fixture.Select(scalarMeta,[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{"(0xA8109CFA,1,128,1)"}},brinMeta()),
    fixture.Select(scalarMeta,[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{nil}},make([]byte,8192)),
    fixture.Select(scalarMeta,[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{nil}},nil),
   }},
   {"revmap",revmap+" WHERE false",[]fixture.Step{
    fixture.Select(revmap,[]string{"pages"},[]string{"TID"},revRows,brinImage(0xf092)),
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
    s=brinMetadata(s,b.PG,false);s.Bag=true
    brinBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   }
   empty:=family.cases[0];empty.Query=family.empty;empty.Rows=nil
   brinBoth(t,p,b,c,brinMetadata(empty,b.PG,false))
  })}
 })
}
