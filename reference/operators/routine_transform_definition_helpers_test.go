package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "strconv"
 "strings"
 "testing"
 "unicode/utf8"

 "stuntgres"
 "stuntgres/reference/fixture"
)

// All callbacks are real registered capabilities. Registration does not promise
// purity: each test explicitly annotates the pure converters through SQL.
func definitionProvider(r *transformRuntime) stuntgres.HostLanguageDefinition {
 d := r.definition()
 d.Provider = "reference-transform-definition"
 // Count entry into the existing genuine wait program without changing its
 // executable computation, cooperative gate, ABI or registration volatility.
 for i:=range d.Programs {
  if d.Programs[i].ID!="wait-v1" {continue}
  execute:=d.Programs[i].Execute
  d.Programs[i].Execute=func(ctx context.Context,args []stuntgres.HostObject)(stuntgres.HostObject,error){r.body.Add(1);return execute(ctx,args)}
 }
 for _, id := range []string{"a", "b", "scaled", "projected"} {
  conversion := stuntgres.HostConversionDefinition{ID:"definition-"+id, Type:stuntgres.HostHstore,
   FromName:stuntgres.HostName{Schema:"public",Name:"td_"+id+"_from"},
   ToName:stuntgres.HostName{Schema:"public",Name:"td_"+id+"_to"},
   FromSQL:hostFromMap, ToSQL:hostToMap}
  if id == "scaled" {
   conversion.FromSQL = func(ctx context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject,error) {
    object,err:=hostFromMap(ctx,v)
    if err!=nil || object.Null { return object,err }
    m:=object.Value.(stuntgres.HostHstoreMap)
    if a,ok:=m["a"]; ok && !a.Null {
     n,err:=strconv.Atoi(a.Text); if err!=nil { return stuntgres.HostObject{},err }
     m["a"]=stuntgres.HostHstoreValue{Text:strconv.Itoa(n*10)}
    }
    return object,nil
   }
  }
  if id == "projected" {
   conversion.ToSQL = func(ctx context.Context, object stuntgres.HostObject) (stuntgres.HostSQLValue,error) {
    value,err:=hostToMap(ctx,object)
    if err!=nil || value.Null { return value,err }
    value.Hstore=append(value.Hstore,stuntgres.HostHstoreEntry{Key:"z",Value:"out"})
    return value,nil
   }
  }
  d.Conversions=append(d.Conversions,conversion)
 }
 for _, direction := range []string{"from", "to"} {
  d.Programs=append(d.Programs,stuntgres.HostProgramDefinition{
   ID:"definition-"+direction,Version:"1",Inputs:[]stuntgres.HostType{stuntgres.HostHstore},Result:stuntgres.HostHstore,
   Execute:func(ctx context.Context,args []stuntgres.HostObject)(stuntgres.HostObject,error) {
    if direction=="from" {
     value,err:=hostToMap(ctx,args[0]); if err!=nil { return stuntgres.HostObject{},err }
     return hostFromEntries(ctx,value)
    }
    value,err:=hostToEntries(ctx,args[0]); if err!=nil { return stuntgres.HostObject{},err }
    return hostFromMap(ctx,value)
   },
  })
 }
 d.Programs=append(d.Programs,stuntgres.HostProgramDefinition{
  ID:"definition-mode",Version:"1",Inputs:[]stuntgres.HostType{stuntgres.HostHstore},Result:stuntgres.HostHstore,
  Execute:func(ctx context.Context,args []stuntgres.HostObject)(stuntgres.HostObject,error) {
   if len(args)!=1 {return stuntgres.HostObject{},errors.New("definition: wrong arity")}
   if args[0].Null {return stuntgres.HostObject{Null:true},nil}
   if entries,ok:=args[0].Value.([]stuntgres.HostHstoreEntry);ok {
    object,err:=hostFromMap(ctx,stuntgres.HostSQLValue{Type:stuntgres.HostHstore,Hstore:entries});if err!=nil{return stuntgres.HostObject{},err}
    object.Value.(stuntgres.HostHstoreMap)["mode"]=stuntgres.HostHstoreValue{Text:"entries"}
    return hostIncrement(ctx,[]stuntgres.HostObject{object})
   }
   return hostIncrement(ctx,args)
  },
 })
 d.Programs=append(d.Programs,stuntgres.HostProgramDefinition{
  ID:"definition-bad-to",Version:"1",Inputs:[]stuntgres.HostType{stuntgres.HostHstore},Result:stuntgres.HostHstore,
  Execute:func(ctx context.Context,args []stuntgres.HostObject)(stuntgres.HostObject,error) {
   m,err:=hostMapObject(args); if err!=nil { return stuntgres.HostObject{},err }
   if _,bad:=m["bad"]; bad { return stuntgres.HostObject{Value:7},nil }
   return hostIncrement(ctx,args)
  },
 })
 return d
}

func definitionScenario(t *testing.T, body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {
 t.Helper()
 definitionScenarioRuntime(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,_ *transformRuntime){body(t,p,b,c)})
}
func definitionScenarioRuntime(t *testing.T, body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn,*transformRuntime)) {
 t.Helper()
 r:=new(transformRuntime)
 p:=fixture.NewScenarioWithDriverConfig(t,func(d *stuntgres.Driver) error { return d.RegisterHostLanguage(definitionProvider(r)) })
 for _,b:=range p.Backends() {
  t.Run(b.Name,func(t *testing.T) {
   c,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer c.Close()
   defer routineLanguagesCleanup(t,c,"DROP LANGUAGE IF EXISTS td_lang CASCADE","DROP EXTENSION IF EXISTS hstore_plpython3u CASCADE","DROP EXTENSION IF EXISTS plpython3u CASCADE")()
   hostExec(t,p,b,c,"CREATE EXTENSION hstore")
   handler:="go_host_call_handler"
   if b.PG {
    hostExec(t,p,b,c,"CREATE EXTENSION plpython3u")
    hostExec(t,p,b,c,"CREATE EXTENSION hstore_plpython3u")
    handler="plpython3_call_handler"
    // These are actual released C symbols, not native aliases or SQL stubs.
    for _,id:=range []string{"a","b"} {
     hostExec(t,p,b,c,"CREATE FUNCTION td_"+id+"_from(internal) RETURNS internal AS '$libdir/hstore_plpython3', 'hstore_to_plpython' LANGUAGE C IMMUTABLE STRICT")
     hostExec(t,p,b,c,"CREATE FUNCTION td_"+id+"_to(internal) RETURNS hstore AS '$libdir/hstore_plpython3', 'plpython_to_hstore' LANGUAGE C IMMUTABLE STRICT")
    }
   }
   hostExec(t,p,b,c,"CREATE LANGUAGE td_lang HANDLER "+handler)
   if !b.PG {
    // A genuine new host alias bootstraps a pair. Remove that unused definition;
    // do not hide duplicates with IF NOT EXISTS or alter bootstrap semantics.
    hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang")
   }
   body(t,p,b,c,r)
  })
 }
}

func definitionAnnotate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 if b.PG { return } // Released C witnesses already explicitly IMMUTABLE.
 for _,name:=range []string{"go_host_from_hstore","go_host_to_hstore","td_a_from","td_a_to","td_b_from","td_b_to","td_scaled_from","td_projected_to"} {
  hostExec(t,p,b,c,"ALTER FUNCTION "+name+"(internal) IMMUTABLE")
 }
}
func definitionCreate(from,to string,replace bool) string {
 q:="CREATE "
 if replace { q+="OR REPLACE " }
 q+="TRANSFORM FOR hstore LANGUAGE td_lang ("
 clauses:=[]string{}
 if from!="" { clauses=append(clauses,"FROM SQL WITH FUNCTION "+from+"(internal)") }
 if to!="" { clauses=append(clauses,"TO SQL WITH FUNCTION "+to+"(internal)") }
 return q+strings.Join(clauses,", ")+")"
}
func definitionFunction(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,name,program,python string) {
 t.Helper()
 hostExec(t,p,b,c,"CREATE FUNCTION "+name+"(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,program,python)))
}
func definitionPair(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,from,to string) int64 {
 t.Helper()
 var oid int64
 rows,err:=c.QueryContext(p.Ctx,"SELECT t.oid::bigint FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype='hstore'::regtype AND l.lanname='td_lang'")
 if err!=nil { t.Fatal(err) }
 if !rows.Next() { rows.Close(); t.Fatal("missing transform catalog row") }
 if err=rows.Scan(&oid); err!=nil { rows.Close(); t.Fatal(err) }
 if rows.Next() { t.Error("duplicate transform catalog row") }
 if err=rows.Err(); err!=nil { t.Error(err) }
 if err=rows.Close(); err!=nil { t.Fatal(err) }
 if oid<=0 { t.Fatalf("invalid transform OID %d",oid) }
 for _,direction:=range []struct{column,name string}{{"trffromsql",from},{"trftosql",to}} {
  q:="SELECT "+direction.column+"=0 AS zero, "+direction.column+" IS NULL AS n FROM pg_transform WHERE oid=$1::oid"
  hostRows(t,p,b,c,q,[]string{"zero","n"},[]string{"BOOL","BOOL"},[][]any{{direction.name=="",false}},oid)
  if direction.name!="" {
   hostRows(t,p,b,c,"SELECT p.proname AS name FROM pg_transform t JOIN pg_proc p ON p.oid=t."+direction.column+" WHERE t.oid=$1::oid",[]string{"name"},[]string{"NAME"},[][]any{{direction.name}},oid)
  }
 }
 return oid
}
func definitionEdges(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,oid int64,from,to string) {
 t.Helper()
 // Compare logical identity, not allocator OIDs or dependency traversal order.
 want:=[][]any{{"pg_language","td_lang"},{"pg_type","hstore"}}
 if from!="" { want=append(want,[]any{"pg_proc",from}) }
 if to!="" { want=append(want,[]any{"pg_proc",to}) }
 q:=`SELECT d.refclassid::regclass::text AS class, CASE WHEN d.refclassid='pg_type'::regclass THEN ty.typname WHEN d.refclassid='pg_language'::regclass THEN l.lanname ELSE p.proname END AS name FROM pg_depend d LEFT JOIN pg_type ty ON d.refclassid='pg_type'::regclass AND ty.oid=d.refobjid LEFT JOIN pg_language l ON d.refclassid='pg_language'::regclass AND l.oid=d.refobjid LEFT JOIN pg_proc p ON d.refclassid='pg_proc'::regclass AND p.oid=d.refobjid WHERE d.classid='pg_transform'::regclass AND d.objid=$1::oid AND d.deptype='n'`
 step:=fixture.Select(q,[]string{"class","name"},[]string{"TEXT","NAME"},want,oid); step.Bag=true
 fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
}
func definitionAbsent(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype='hstore'::regtype AND l.lanname='td_lang'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
}
func definitionDiagnostic(t *testing.T,p *fixture.Pair,c *sql.Conn,q string,want fixture.Diagnostic,preparedPhase string) {
 t.Helper()
 for _,prepared:=range []bool{false,true} {
  phase:="execute"
  var err error
  if prepared {
   var stmt *sql.Stmt
   stmt,err=c.PrepareContext(p.Ctx,q)
   if err!=nil { phase="prepare" } else { _,err=stmt.ExecContext(p.Ctx); stmt.Close() }
  } else { _,err=c.ExecContext(p.Ctx,q) }
  if err==nil { t.Errorf("%q prepared=%v unexpectedly succeeded",q,prepared); continue }
  expectedPhase:="execute"; if prepared { expectedPhase=preparedPhase }
  if phase!=expectedPhase { t.Errorf("%q prepared=%v phase=%s want %s",q,prepared,phase,expectedPhase) }
  got:=fixture.ExtractDiagnostic(t,err)
  if got!=want { t.Errorf("%q prepared=%v phase=%s full diagnostic=%#v; independently expected=%#v",q,prepared,phase,got,want) }
 }
}
func definitionSyntaxDiagnostic(q,token string) fixture.Diagnostic {
 d:=fixture.ControlsReferenceDiagnostic("42601","syntax error at or near \""+token+"\"")
 index:=strings.LastIndex(q,token)
 d.Position=strconv.Itoa(utf8.RuneCountInString(q[:index])+1)
 return d
}
func definitionOrdinaryError(t *testing.T,err error,message string) {
 t.Helper()
 if err==nil || !strings.Contains(err.Error(),message) { t.Errorf("ordinary callback error=%v want %q",err,message) }
 var state interface{SQLState() string}
 if errors.As(err,&state) { t.Errorf("ordinary callback error fabricated SQLSTATE: %v",err) }
}
