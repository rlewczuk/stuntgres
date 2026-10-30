package core

import (
 "fmt"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

var relationStatsKeys = [...]string{"schemaname","relname","relpages","reltuples","relallvisible","relallfrozen"}
var relationStatsTypes = [...]sqltype.ID{sqltype.Text,sqltype.Text,sqltype.Int4,sqltype.Float4,sqltype.Int4,sqltype.Int4}
type relationStatsArguments [6]sqltype.Value
func(a relationStatsArguments) has(i int)bool{return a[i].Type()!=nil&&!a[i].IsNull()}
func(a relationStatsArguments) required(i int)error{if !a.has(i){return SQLError("22023",fmt.Sprintf(`argument "%s" must not be null`,relationStatsKeys[i]))};return nil}
func(h *statementHost) relationStatsWarning(d sqlstate.Error){d.Severity,d.SeverityUnlocalized="WARNING","WARNING";h.scope().Op.EmitNotice(session.LevelWarning,d)}
func(h *statementHost) decodeRelationStats(values []sqltype.Value,variadic bool)(relationStatsArguments,bool,error){
 var args relationStatsArguments
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
  if relationStatsNameEqual(name,"version"){continue}
  slot:=-1;for j,k:=range relationStatsKeys{if relationStatsNameEqual(name,k){slot=j;break}}
  if slot<0{h.relationStatsWarning(sqlstate.Error{Code:"01000",Message:fmt.Sprintf(`unrecognized argument name: "%s"`,name)});result=false;continue}
  if value.Type().ID()!=relationStatsTypes[slot]{
   expected:="real[]";switch relationStatsTypes[slot]{case sqltype.Text:expected="text";case sqltype.Int2:expected="smallint";case sqltype.Bool:expected="boolean";case sqltype.Float4:expected="real";case sqltype.Int4:expected="integer"}
   h.relationStatsWarning(sqlstate.Error{Code:"01000",Message:fmt.Sprintf(`argument "%s" has type %s, expected type %s`,name,value.Type().DiagnosticName(),expected)});result=false;continue
  }
  args[slot]=value
 }
 return args,result,nil
}

// Released pg_strcasecmp under C/UTF8 folds ASCII bytes, not Unicode runes.
// All declared keys are lowercase ASCII.
func relationStatsNameEqual(input, key string) bool {
 if len(input)!=len(key){return false}
 for i:=range len(input) {
  c:=input[i]
  if c>='A'&&c<='Z'{c+='a'-'A'}
  if c!=key[i]{return false}
 }
 return true
}
