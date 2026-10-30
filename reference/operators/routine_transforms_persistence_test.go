package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "io"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func transformsOpen(t *testing.T,r *transformRuntime,mutate func(*stuntgres.HostLanguageDefinition))(*sql.DB,fixture.Backend){
 t.Helper()
 driver:=stuntgres.NewDriver()
 if mutate==nil{
  if err:=registerTransforms(driver,r);err!=nil{t.Fatal(err)}
 }else{
  d:=r.definition();mutate(&d)
  if d.Provider!=""{if err:=driver.RegisterHostLanguage(d);err!=nil{t.Fatal(err)}}
  for _,direction:=range []string{"from","to","none","unavailable"}{if err:=driver.RegisterHostLanguage(r.directionDefinition(direction));err!=nil{t.Fatal(err)}}
 }
 connector,err:=driver.OpenConnector("transforms-restore")
 if err!=nil{t.Fatal(err)}
 return hostNativePools(t,connector)
}
func transformsPersistentResults(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 hostBag(t,p,b,c,"tr_persist('a=>7,b=>NULL'::hstore)",transformsMapRows)
 hostBag(t,p,b,c,"tr_persist_default('a=>7,b=>NULL'::hstore)",transformsDefaultRows)
 transformsAssociation(t,p,b,c,"tr_persist",2)
 transformsAssociation(t,p,b,c,"tr_persist_default",0)
 transformsPairEdges(t,p,b,c,"tr_persist",1)
 hostRows(t,p,b,c,"SELECT v FROM tr_persist_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(71)}})
 hostMetadata(t,p,b,c,"SELECT tr_persist('a=>7'::hstore) AS v")
 transformsCall(t,p,b,c,"CALL tr_persist_call(NULL::hstore)","v",[][]any{{nil}})
 rows,err:=c.QueryContext(p.Ctx,"CALL tr_persist_call('a=>7,b=>NULL'::hstore)");if err!=nil{t.Fatal(err)}
 if !rows.Next(){t.Fatal("missing persistent CALL row")};var raw string;if err=rows.Scan(&raw);err!=nil{t.Fatal(err)};if rows.Next(){t.Fatal("extra persistent CALL row")};if err=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
 hostBag(t,p,b,c,"$1::hstore",transformsMapRows,raw)
 hostRows(t,p,b,c,"SELECT proacl IS NOT NULL AS acl FROM pg_proc WHERE proname='tr_persist'",[]string{"acl"},[]string{"BOOL"},[][]any{{true}})
}
func transformsDump(t *testing.T,ctx context.Context,db *sql.DB)[]byte{
 t.Helper();var stream bytes.Buffer
 if err:=stuntgres.Dump(ctx,db,&stream);err!=nil{t.Fatal(err)}
 return stream.Bytes()
}
type transformsBrokenWriter struct{}
func (transformsBrokenWriter) Write([]byte)(int,error){return 0,io.ErrClosedPipe}
type transformsBrokenReader struct{ prefix *bytes.Reader }
func (r transformsBrokenReader) Read(p []byte)(int,error){n,e:=r.prefix.Read(p);if e==io.EOF{return n,io.ErrUnexpectedEOF};return n,e}

func TestReferenceRoutineTransformsPersistence(t *testing.T){
 transformsScenario(t,transformsPersistence)
}
func transformsPersistence(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
 transformsPersistenceWithRoles(t,p,b,c,r,nil)
}
func transformsPersistenceWithRoles(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime,roles []string){
 transformsFunction(t,p,b,c,"tr_persist","selection-v1",transformsSelectionPython,"FOR TYPE hstore, FOR TYPE hstore","")
 transformsFunction(t,p,b,c,"tr_persist_default","selection-v1",transformsSelectionPython,"","")
 transformsProcedure(t,p,b,c,"tr_persist_call","INOUT v hstore","if v is None:\n    return (None,)\nv['a']=str(int(v['a'])+1)\nv['c']='3'\nreturn (v,)","FOR TYPE hstore")
 hostExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION tr_persist(hstore) FROM PUBLIC")
 hostExec(t,p,b,c,"CREATE TABLE tr_persist_sentinel(v integer)")
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO tr_persist_sentinel VALUES (71)",1))
 transformsPersistentResults(t,p,b,c)
 // Native controls are not PostgreSQL SQL operations. The PG arm still
 // independently exercises every persistent object's SQL behavior above.
 if b.PG{return}
 stream:=transformsDump(t,p.Ctx,p.Native)
 if !strings.Contains(string(stream),"TRANSFORM FOR TYPE") {t.Fatal("dump lost explicit association")}
 sourceCount:=r.body.Load()
 for range 2{
  fresh:=new(transformRuntime)
  db,destination:=transformsOpen(t,fresh,nil)
  // Dump references cluster roles; it never creates destination principals.
  for _,role:=range roles{hostExec(t,p,destination,destination.DB,"CREATE ROLE "+role)}
  if err:=stuntgres.Restore(p.Ctx,db,bytes.NewReader(stream));err!=nil{t.Fatal(err)}
  target,err:=destination.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)}
  transformsPersistentResults(t,p,destination,target)
  for _,role:=range roles {
   hostRows(t,p,destination,target,"SELECT has_schema_privilege($1,'public','USAGE') AS schema_ok,has_type_privilege($1,'hstore','USAGE') AS type_ok,has_function_privilege($1,'tr_replace(hstore)','EXECUTE') AS execute_ok",[]string{"schema_ok","type_ok","execute_ok"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,false}},role)
  }
  q:="SELECT key,value FROM each(tr_persist($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,target,q)
  hostRows(t,p,destination,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  if fresh.body.Load()==0||fresh.from.Load()==0||fresh.to.Load()==0{t.Fatal("destination callbacks not used")}
  if r.body.Load()!=sourceCount{t.Fatal("destination leaked source callback token")}
  target.Close()
 }
 for _,mode:=range []string{"absent","abi","program","type","direction","default","late-association","reader","canceled"}{
  t.Run(mode,func(t *testing.T){
   mutate:=func(d *stuntgres.HostLanguageDefinition){
    switch mode{
    case "absent":d.Provider=""
    case "abi":d.ABIVersion="different"
    case "program":d.Programs[0].Version="different"
    case "type":d.Programs[0].Inputs[0]=stuntgres.HostText
    case "direction":d.Conversions[0].FromSQL=nil;d.Conversions[0].FromName=stuntgres.HostName{}
    case "default":d.Defaults[0].FromID=""
    }
   }
   targetDB,destination:=transformsOpen(t,new(transformRuntime),mutate)
   for _,role:=range roles{hostExec(t,p,destination,destination.DB,"CREATE ROLE "+role)}
   target,err:=destination.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer target.Close()
   hostExec(t,p,destination,target,"CREATE TABLE tr_restore_sentinel(v integer)")
   fixture.RunStep(t,p.Ctx,target,destination.Recorder,fixture.Exec("INSERT INTO tr_restore_sentinel VALUES (99)",1))
   q:="SELECT v FROM tr_restore_sentinel"
   held:=transformsHeld(t,p,target,q)
   before:=transformsDump(t,p.Ctx,targetDB)
   data:=stream
   if mode=="late-association"{data=append(append([]byte{},stream...),[]byte("\nCREATE FUNCTION tr_late(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE integer AS 'selection-v1';\n")...)}
   var reader io.Reader=bytes.NewReader(data)
   if mode=="reader"{reader=transformsBrokenReader{bytes.NewReader(data)}}
   ctx:=p.Ctx
   if mode=="canceled"{var cancel context.CancelFunc;ctx,cancel=context.WithCancel(ctx);cancel()}
   if err:=stuntgres.Restore(ctx,targetDB,reader);err==nil{t.Fatal("invalid replay published")}
   hostRows(t,p,destination,held,q,[]string{"v"},[]string{"INT4"},[][]any{{int64(99)}})
   after:=transformsDump(t,p.Ctx,targetDB)
   if !bytes.Equal(before,after){t.Fatal("failed replay changed target dump")}
  })
 }
 if err:=stuntgres.Dump(p.Ctx,p.Native,transformsBrokenWriter{});!errors.Is(err,io.ErrClosedPipe){t.Fatalf("writer error=%v",err)}
 if after:=transformsDump(t,p.Ctx,p.Native);!bytes.Equal(stream,after){t.Fatal("failed dump changed source")}
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(stream));err!=nil{t.Fatal(err)}
 transformsPersistentResults(t,p,b,c)
 transformsPairAbsence(t,p)
}

func TestReferenceRoutineTransformsNativeBoundary(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){
  transformsFunction(t,p,b,c,"tr_wrong_object","wrong-object-v1","return 7","FOR TYPE hstore","")
  if b.PG{
   d:=fixture.ControlsReferenceDiagnostic("42809","not a Python mapping")
   d.Context="while creating return value\nPL/Python function \"tr_wrong_object\""
   hostError(t,p,c,"SELECT tr_wrong_object('a=>7'::hstore)",d)
  }else{
   hostNativeError(t,p.Ctx,c,"SELECT tr_wrong_object('a=>7'::hstore)","host: result is not a nonnil map")
   hostNativeError(t,p.Ctx,c,"SELECT tr_wrong_object('from_wrong_object=>1'::hstore)","host: result is not a nonnil map")
   for _,formals:=range []string{"v hstore","v hstore, OUT a hstore, OUT b hstore"}{
    _,err:=c.ExecContext(p.Ctx,"CREATE PROCEDURE tr_unsupported("+formals+") LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'procedure-v1'")
    if err==nil{t.Fatal("unsupported void/multi-output host ABI admitted")}
    d:=fixture.ExtractDiagnostic(t,err);if d.Code!="0A000"{t.Errorf("unsupported native shape=%#v",d)}
   }
  }
 })
}

func TestReferenceRoutineTransformsFeatureSmoke(t *testing.T){
 transformsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
  transformsReassociation(t,p,b,c,r)
  transformsDiagnostic(t,p,c,"ALTER FUNCTION tr_replace(hstore) TRANSFORM FOR TYPE hstore","42601","syntax error at or near \"TRANSFORM\"")
  defer routineLanguagesCleanup(t,c,"RESET ROLE","DROP OWNED BY tr_smoke_reader CASCADE","DROP ROLE tr_smoke_reader")()
  hostExec(t,p,b,c,"CREATE ROLE tr_smoke_reader")
  hostExec(t,p,b,c,"GRANT USAGE ON SCHEMA public TO tr_smoke_reader")
  hostExec(t,p,b,c,"GRANT USAGE ON TYPE hstore TO tr_smoke_reader")
  hostExec(t,p,b,c,"SET ROLE tr_smoke_reader")
  transformsDiagnostic(t,p,c,"SELECT tr_replace('a=>7'::hstore)","42501","permission denied for function tr_replace")
  hostExec(t,p,b,c,"RESET ROLE")
  transformsPersistenceWithRoles(t,p,b,c,r,[]string{"tr_smoke_reader"})
 })
}
