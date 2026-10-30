package reference_test

import (
 "fmt"
 "strings"
 "testing"
 "unicode/utf8"
 "stuntgres/reference/fixture"
)
func TestReferenceDatabaseFilesDiagnostics(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  const hint="No function matches the given name and argument types. You might need to add explicit type casts."
  type sample struct{q,code,message,hint,at,context string;args []any}
  var cases []sample
  for _,f:=range databaseFilesFunctions{
   name:=f.name;valid:="0::regclass";booleans:="true";types:="boolean";extra:="1,2";extraTypes:="integer, integer";named:="relation => 0";namedTypes:="relation => integer"
   if f.oid==3454{valid="0::oid,0::oid";booleans="true,true";types="boolean, boolean";extra="1,2,3";extraTypes="integer, integer, integer";named="tablespace => 0,filenode => 0";namedTypes="tablespace => integer, filenode => integer"}
   noMatch:=func(call,signature string){cases=append(cases,sample{q:"SELECT "+call,code:"42883",message:"function "+signature+" does not exist",hint:hint,at:call})}
   noMatch(name+"()",name+"()")
   noMatch(name+"("+extra+")",name+"("+extraTypes+")")
   noMatch(name+"("+booleans+")",name+"("+types+")")
   noMatch(name+"("+named+")",name+"("+namedTypes+")")
   noMatch("public."+name+"("+booleans+")","public."+name+"("+types+")")
   noMatch(`"`+strings.ToUpper(name)+`"(`+booleans+")",strings.ToUpper(name)+"("+types+")")
   noMatch(name+"_missing("+booleans+")",name+"_missing("+types+")")
   cases=append(cases,sample{q:"SELECT /* é界 */ "+name+"("+booleans+")",code:"42883",message:"function "+name+"("+types+") does not exist",hint:hint,at:name})
   cases=append(cases,sample{q:"SELECT "+name+"(DISTINCT "+booleans+")",code:"42883",message:"function "+name+"("+types+") does not exist",hint:hint,at:name})
   if f.oid==3454{noMatch(name+"('0'::text,'0'::text)",name+"(text, text)");noMatch(name+"(0::oid)",name+"(oid)")}
   for _,d:=range []struct{suffix,message string}{{"DISTINCT ","DISTINCT"},{"ORDER BY 1","ORDER BY"},{"FILTER (WHERE true)","FILTER"},{"OVER ()","OVER"}}{
    call:=name+"("+valid+")";message:=d.message+" specified, but "+name+" is not an aggregate function"
    switch d.message{case "DISTINCT":call=name+"(DISTINCT "+valid+")";case "ORDER BY":call=name+"("+valid+" ORDER BY 1)";case "FILTER":call+=" "+d.suffix;case "OVER":call+=" "+d.suffix;message="OVER specified, but "+name+" is not a window function nor an aggregate function"}
    cases=append(cases,sample{q:"SELECT "+call,code:"42809",message:message,at:name})
   }
   cases=append(cases,sample{q:"SELECT "+name+"("+strings.Repeat("1,",100)+"1)",code:"54023",message:"cannot pass more than 100 arguments to a function",at:name})
   if f.oid!=3454{
    cases=append(cases,sample{q:"SELECT "+name+"('database_files_missing')",code:"42P01",message:`relation "database_files_missing" does not exist`,at:"'database_files_missing'"},sample{q:"SELECT "+name+"($1)",code:"42P01",message:`relation "database_files_missing" does not exist`,context:`unnamed portal parameter $1 = '...'`,args:[]any{"database_files_missing"}})
   }
  }
  for position:=1;position<=2;position++{for _,input:=range []string{"bad","4294967296"}{
   code:="22P02";message:=`invalid input syntax for type oid: "bad"`;if input!="bad"{code="22003";message=`value "4294967296" is out of range for type oid`}
   args:="'"+input+"',0::oid";params:=[]any{input,int64(0)};if position==2{args="0::oid,'"+input+"'";params=[]any{int64(0),input}}
   for _,prefix:=range []string{"SELECT ","SELECT /* é界 */ "}{cases=append(cases,sample{q:prefix+"pg_filenode_relation("+args+")",code:code,message:message,at:"'"+input+"'"})}
   cases=append(cases,sample{q:"SELECT pg_filenode_relation($1,$2)",code:code,message:message,context:fmt.Sprintf("unnamed portal parameter $%d = '...'",position),args:params})
  }}
  for _,tc:=range cases{for _,prepared:=range []bool{false,true}{
   position:="";if tc.at!=""{index:=strings.Index(tc.q,tc.at);if index<0{t.Fatal(tc)};position=fixture.Position(utf8.RuneCountInString(tc.q[:index])+1)}
   s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:position,Context:tc.context},tc.args...);s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";if prepared&&len(tc.args)==0{s.ErrorStage="prepare"};databaseFilesRun(t,p,b,s)
  }}
  for _,f:=range databaseFilesFunctions{
   q:="SELECT "+f.name+"($1) AS v";bad:=[]any{"database_files_missing"};good:=[]any{nil};d:=fixture.Diagnostic{Code:"42P01",Message:`relation "database_files_missing" does not exist`,Context:`unnamed portal parameter $1 = '...'`}
   if f.oid==3454{q="SELECT pg_filenode_relation($1,$2) AS v";bad=[]any{"bad",int64(0)};good=[]any{int64(0),int64(0)};d.Code="22P02";d.Message=`invalid input syntax for type oid: "bad"`}
   stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
   s:=fixture.ImplicitError(q,d,bad...);s.SelectRows=true;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   fixture.RunStep(t,p.Ctx,held,b.Recorder,databaseFilesNull(q,"v",f.result,good...));stmt.Close()
  }
 })}
}
