package reference_test

import (
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

var databaseFilesFunctions = []struct{name,formal,result string;oid int}{
 {"pg_relation_filenode","regclass","OID",2999},
 {"pg_relation_filepath","regclass","TEXT",3034},
 {"pg_filenode_relation","oid,oid","REGCLASS",3454},
}
func databaseFilesRun(t *testing.T,p *fixture.Pair,b fixture.Backend,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
func databaseFilesNull(q,name,typ string,args ...any)fixture.Step{return fixture.Select(q,[]string{name},[]string{typ},[][]any{{nil}},args...)}
func databaseFilesBoundary(q,name string)fixture.Step{
 message:="pg_relation_filenode is not supported: the embedded engine has no physical PostgreSQL relation filenode mapping"
 if name=="pg_relation_filepath" {message="pg_relation_filepath is not supported: the embedded engine has no physical PostgreSQL relation file paths"}
 s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"0A000",Message:message});s.SelectRows=true;s.ErrorStage="execute";return s
}
func TestReferenceDatabaseFilesValues(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  for _,f:=range databaseFilesFunctions {
   for _,prepared:=range []bool{false,true}{for _,prefix:=range []string{"","pg_catalog."}{
    calls:=[]string{"NULL","NULL::regclass","0::regclass"}
    if f.oid==3454 {calls=[]string{"NULL,NULL","NULL::oid,0::oid","0::oid,NULL::oid","0::oid,0::oid","1663::oid,0::oid","4294967295::oid,0::oid"}}
    for _,args:=range calls{s:=databaseFilesNull("SELECT "+prefix+f.name+"("+args+")",f.name,f.result);s.Prepared=prepared;databaseFilesRun(t,p,b,s)}
   }}
   q:="SELECT "+f.name+"($1) AS v";args:=[]any{nil}
   if f.oid==3454{q="SELECT "+f.name+"($1,$2) AS v";args=append(args,int64(0))}
   stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
   held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
   fixture.RunStep(t,p.Ctx,held,b.Recorder,databaseFilesNull(q,"v",f.result,args...))
   args[0]=int64(0);fixture.RunStep(t,p.Ctx,held,b.Recorder,databaseFilesNull(q,"v",f.result,args...))
   if f.oid==3454{
    for _,pair:=range [][]any{{int64(0),nil},{nil,nil}}{fixture.RunStep(t,p.Ctx,held,b.Recorder,databaseFilesNull(q,"v",f.result,pair...))}
   }
   stmt.Close()
  }
 })}
}
func TestReferenceDatabaseFilesMetadata(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  databaseFilesRun(t,p,b,fixture.Select("SELECT oid::bigint AS oid,proname FROM pg_proc WHERE proname IN ('pg_relation_filenode','pg_relation_filepath','pg_filenode_relation') ORDER BY oid",[]string{"oid","proname"},[]string{"INT8","NAME"},[][]any{{int64(2999),"pg_relation_filenode"},{int64(3034),"pg_relation_filepath"},{int64(3454),"pg_filenode_relation"}}))
  for _,f:=range databaseFilesFunctions{
   args:="NULL";formals:="2205";result:=int64(26);nargs:=int64(1)
   if f.oid==3034{result=25};if f.oid==3454{args="NULL,NULL";formals="26 26";result=2205;nargs=2}
   for _,prepared:=range []bool{false,true}{
    s:=fixture.Select("SELECT "+f.name+"("+args+") WHERE FALSE",[]string{f.name},[]string{f.result},nil);s.Prepared=prepared;databaseFilesRun(t,p,b,s)
    s=databaseFilesNull("SELECT * FROM "+f.name+"("+args+")",f.name,f.result);s.Prepared=prepared;databaseFilesRun(t,p,b,s)
   }
   databaseFilesRun(t,p,b,fixture.Select(fmt.Sprintf("SELECT proname,pronargs,prorettype::bigint AS result,proisstrict,provolatile,proparallel,prokind,pronamespace::bigint AS ns FROM pg_proc WHERE oid=%d",f.oid),[]string{"proname","pronargs","result","proisstrict","provolatile","proparallel","prokind","ns"},[]string{"NAME","INT2","INT8","BOOL","CHAR","CHAR","CHAR","INT8"},[][]any{{f.name,nargs,result,true,"s","s","f",int64(11)}}))
   databaseFilesRun(t,p,b,fixture.Select(fmt.Sprintf("SELECT proargtypes::text AS args,proretset,prosecdef,proargnames,pronargdefaults,provariadic::bigint AS variadic,prosqlbody IS NOT NULL AS body,prosrc,procost,prolang::bigint AS lang FROM pg_proc WHERE oid=%d",f.oid),[]string{"args","proretset","prosecdef","proargnames","pronargdefaults","variadic","body","prosrc","procost","lang"},[]string{"TEXT","BOOL","BOOL","_TEXT","INT2","INT8","BOOL","TEXT","FLOAT4","INT8"},[][]any{{formals,false,false,nil,int64(0),int64(0),false,f.name,float64(1),int64(12)}}))
  }
 })}
}
