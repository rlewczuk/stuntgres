package reference_test

import (
 "encoding/base64"
 "encoding/json"
 "slices"
 "strings"
)

// Mutations replace their genuine record; duplicate-key rejection must never
// mask semantic admission. Each failed import preserves destination data/stats.
func attributeStatsMalformedSnapshots(s attributeStatsScenario,script string)[]string{
 s.t.Helper()
 const prefix="-- stuntgres-attribute-statistics-v1 "
 lines:=strings.Split(script,"\n")
 records:=make(map[string][]byte);indexes:=make(map[string]int)
 for i,line:=range lines{
  if !strings.HasPrefix(line,prefix){continue}
  raw,err:=base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(line,prefix));if err!=nil{s.t.Fatal(err)}
  var record map[string]any;if err=json.Unmarshal(raw,&record);err!=nil{s.t.Fatal(err)}
  column,_:=record["Column"].(string)
  if record["Inherited"]==false&&(record["Relation"]=="ast_t"||record["Relation"]=="ast_snapshot_targets"){records[column],indexes[column]=raw,i}
 }
 decode:=func(column string)map[string]any{
  raw,ok:=records[column];if !ok{s.t.Fatal("missing genuine snapshot mutation target",column)}
  var record map[string]any;if err:=json.Unmarshal(raw,&record);err!=nil{s.t.Fatal(err)};return record
 }
 firstSlot:=func(record map[string]any)map[string]any{
  slots,_:=record["Slots"].([]any);if len(slots)==0{s.t.Fatal("missing occupied MCV slot")};return slots[0].(map[string]any)
 }
 var scripts []string
 for _,kind:=range []string{"kind","type","slot-count","scalar-dechist","scalar-mcelem","json-dechist","mcv-typmod","mcv-collation"}{
  column:="a";if kind=="json-dechist"{column="j"};if kind=="mcv-typmod"||kind=="mcv-collation"{column="v"}
  record:=decode(column)
  switch kind{
  case "kind":firstSlot(record)["kind"]=99
  case "type":usage:=record["Usage"].(map[string]any);usage["type_schema"],usage["type_name"]="pg_catalog","text"
  case "slot-count":slot:=firstSlot(record);six:=make([]any,6);for i:=range six{six[i]=slot};record["Slots"]=six
  case "scalar-dechist","json-dechist":record["Slots"]=[]any{map[string]any{"kind":5,"numbers":firstSlot(decode("a"))["numbers"]}}
  case "scalar-mcelem":
   found:=false
   for _,raw:=range decode("arr")["Slots"].([]any){slot:=raw.(map[string]any);if slot["kind"]==float64(4){record["Slots"]=[]any{slot};found=true;break}}
   if !found{s.t.Fatal("missing genuine element MCV mutation source")}
  case "mcv-typmod":usage:=firstSlot(record)["values"].(map[string]any)["usage"].(map[string]any);usage["typmod"]=-1
  case "mcv-collation":usage:=firstSlot(record)["values"].(map[string]any)["usage"].(map[string]any);usage["collation_schema"],usage["collation_name"]="pg_catalog","default"
  }
  data,err:=json.Marshal(record);if err!=nil{s.t.Fatal(err)}
  changed:=slices.Clone(lines);changed[indexes[column]]=prefix+base64.StdEncoding.EncodeToString(data)
  scripts=append(scripts,strings.Join(changed,"\n"))
 }
 return scripts
}
