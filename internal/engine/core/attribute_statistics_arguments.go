package core

import (
 "fmt"
 "strings"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

var attributeStatsKeys = [...]string{"schemaname","relname","attname","attnum","inherited","null_frac","avg_width","n_distinct","most_common_vals","most_common_freqs","histogram_bounds","correlation","most_common_elems","most_common_elem_freqs","elem_count_histogram","range_length_histogram","range_empty_frac","range_bounds_histogram"}
var attributeStatsTypes = [...]sqltype.ID{sqltype.Text,sqltype.Text,sqltype.Text,sqltype.Int2,sqltype.Bool,sqltype.Float4,sqltype.Int4,sqltype.Float4,sqltype.Text,1021,sqltype.Text,sqltype.Float4,sqltype.Text,1021,1021,sqltype.Text,sqltype.Float4,sqltype.Text}
type attributeStatsArguments [18]sqltype.Value
func(a attributeStatsArguments) has(i int)bool{return a[i].Type()!=nil&&!a[i].IsNull()}
func(a attributeStatsArguments) required(i int)error{if !a.has(i){return SQLError("22023",fmt.Sprintf(`argument "%s" must not be null`,attributeStatsKeys[i]))};return nil}
func(h *statementHost) attributeStatsWarning(d sqlstate.Error){d.Severity,d.SeverityUnlocalized="WARNING","WARNING";h.scope().Op.EmitNotice(session.LevelWarning,d)}
func(h *statementHost) decodeAttributeStats(values []sqltype.Value,variadic bool)(attributeStatsArguments,bool,error){
 var args attributeStatsArguments
 count:=len(values)
 if variadic {if values[0].IsNull(){count=-1}else{count=values[0].ArrayLen()}}
 if count%2!=0{return args,false,&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"XX000",Message:"variadic arguments must be name/value pairs",Hint:"Provide an even number of variadic arguments that can be divided into pairs."}}
 at:=func(i int)sqltype.Value{if variadic{return values[0].ArrayElement(i)};return values[i]}
 result:=true
 for i:=0;i<count;i+=2 {
  key,value:=at(i),at(i+1)
  if key.IsNull(){return args,false,SQLError("XX000",fmt.Sprintf("name at variadic position %d is null",i+1))}
  if key.Type().ID()!=sqltype.Text{return args,false,SQLError("XX000",fmt.Sprintf("name at variadic position %d has type %s, expected type text",i+1,key.Type().DiagnosticName()))}
  if value.IsNull(){continue}
  name,err:=key.Text();if err!=nil{return args,false,err}
  if strings.EqualFold(name,"version"){continue}
  slot:=-1;for j,k:=range attributeStatsKeys{if strings.EqualFold(k,name){slot=j;break}}
  if slot<0{h.attributeStatsWarning(sqlstate.Error{Code:"01000",Message:fmt.Sprintf(`unrecognized argument name: "%s"`,name)});result=false;continue}
  if value.Type().ID()!=attributeStatsTypes[slot]{
   expected:="real[]";switch attributeStatsTypes[slot]{case sqltype.Text:expected="text";case sqltype.Int2:expected="smallint";case sqltype.Bool:expected="boolean";case sqltype.Float4:expected="real";case sqltype.Int4:expected="integer"}
   h.attributeStatsWarning(sqlstate.Error{Code:"01000",Message:fmt.Sprintf(`argument "%s" has type %s, expected type %s`,name,value.Type().DiagnosticName(),expected)});result=false;continue
  }
  args[slot]=value
 }
 return args,result,nil
}
