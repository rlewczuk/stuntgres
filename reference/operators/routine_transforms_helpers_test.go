package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "strconv"
 "strings"
 "sync/atomic"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

// This provider uses the shipped compiled handler and real map/entry converters.
// Its representation-sensitive Program deliberately has no conversion selectors:
// SQL, not a second Program ID, must decide which objects reach the same body.
type transformRuntime struct {
 hostNativeRuntime
 from, body, to atomic.Int64
}

func (r *transformRuntime) definition() stuntgres.HostLanguageDefinition {
 d := r.hostNativeRuntime.definition()
 d.Provider = "reference-routine-transforms"
 d.Conversions[0].FromSQL = func(ctx context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject, error) {
  r.from.Add(1)
  return hostFromMap(ctx, v)
 }
 d.Conversions[0].ToSQL = func(ctx context.Context, v stuntgres.HostObject) (stuntgres.HostSQLValue, error) {
  r.to.Add(1)
  // The language default representation is an entry list, while the selected
  // transform is a map. Both are real conversions, not identity SQL values.
  return hostToMap(ctx, v)
 }
 d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{
  ID: "selection-v1", Version: "1", Inputs: []stuntgres.HostType{stuntgres.HostHstore}, Result: stuntgres.HostHstore,
  Execute: func(ctx context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
   r.body.Add(1)
   if len(args) != 1 { return stuntgres.HostObject{}, errors.New("transforms: wrong arity") }
   if args[0].Null { return stuntgres.HostObject{Value: stuntgres.HostHstoreMap{"n": {Text: "handled"}}}, nil }
   if _, ok := args[0].Value.(stuntgres.HostHstoreMap); ok { return hostIncrement(ctx, args) }
   entries, ok := args[0].Value.([]stuntgres.HostHstoreEntry)
   if !ok { return stuntgres.HostObject{}, errors.New("transforms: unexpected carrier") }
   out := append([]stuntgres.HostHstoreEntry{}, entries...)
   out = append(out, stuntgres.HostHstoreEntry{Key: "default", Value: "entries"})
   return stuntgres.HostObject{Value: out}, nil
  },
 }, stuntgres.HostProgramDefinition{
  ID: "emit-v1", Version: "1", Result: stuntgres.HostHstore,
  Execute: func(context.Context, []stuntgres.HostObject) (stuntgres.HostObject, error) {
   r.body.Add(1)
   return stuntgres.HostObject{Value: stuntgres.HostHstoreMap{"a": {Text: "1"}, "b": {Text: "boo"}, "c": {Null: true}}}, nil
  },
 })
 primitive := hostPrimitiveDefinition()
 for _, conversion := range primitive.Conversions {
  conversion.Bootstrap = false
  d.Conversions = append(d.Conversions, conversion)
 }
 d.Defaults = append(d.Defaults, primitive.Defaults...)
 d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{ID:"one-v1",Version:"1",Result:stuntgres.HostInt4,Execute:func(context.Context,[]stuntgres.HostObject)(stuntgres.HostObject,error){return stuntgres.HostObject{Value:int64(1)},nil}})
 d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{ID:"procedure-v1",Version:"1",Inputs:[]stuntgres.HostType{stuntgres.HostHstore},Result:stuntgres.HostHstore,Execute:func(ctx context.Context,args []stuntgres.HostObject)(stuntgres.HostObject,error){
  if args[0].Null{return stuntgres.HostObject{Null:true},nil}
  return hostIncrement(ctx,args)
 }})
 d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{ID:"count-v1",Version:"1",Inputs:[]stuntgres.HostType{stuntgres.HostHstore},Result:stuntgres.HostInt4,Execute:func(_ context.Context,args []stuntgres.HostObject)(stuntgres.HostObject,error){
  m,err:=hostMapObject(args);if err!=nil{return stuntgres.HostObject{},err}
  return stuntgres.HostObject{Value:int64(len(m))},nil
 }})
 return d
}

func registerTransforms(d *stuntgres.Driver, r *transformRuntime) error {
 if err := d.RegisterHostLanguage(r.definition()); err != nil { return err }
 for _, direction := range []string{"from", "to", "none", "unavailable"} {
  if err := d.RegisterHostLanguage(r.directionDefinition(direction)); err != nil { return err }
 }
 return nil
}

func transformsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *transformRuntime)) {
 t.Helper()
 runtime := new(transformRuntime)
 p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error { return registerTransforms(d, runtime) })
 for _, b := range p.Backends() {
  t.Run(b.Name, func(t *testing.T) {
   c, err := b.DB.Conn(p.Ctx)
   if err != nil { t.Fatal(err) }
   defer c.Close()
   defer routineLanguagesCleanup(t, c, "DROP EXTENSION IF EXISTS hstore_plpython3u CASCADE", "DROP EXTENSION IF EXISTS plpython3u CASCADE")()
   hostExec(t, p, b, c, "CREATE EXTENSION hstore")
   if b.PG {
    hostExec(t, p, b, c, "CREATE EXTENSION plpython3u")
    hostExec(t, p, b, c, "CREATE EXTENSION hstore_plpython3u")
   }
   body(t, p, b, c, runtime)
  })
 }
}

const transformsSelectionPython = "if v is None:\n    return {'n':'handled'}\nif isinstance(v, dict):\n    v['a'] = str(int(v.get('a','0'))+1)\n    v['c'] = '3'\n    return v\nassert isinstance(v,str)\nreturn v + ',\"default\"=>\"entries\"'"

func transformsSource(b fixture.Backend, native, python string) string {
 if b.PG { return python }; return native
}
func transformsLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func transformsFunction(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, program, python, list, options string) {
 t.Helper()
 clause := ""
 if list != "" { clause = " TRANSFORM " + list }
 hostExec(t, p, b, c, "CREATE OR REPLACE FUNCTION "+name+"(v hstore) RETURNS hstore LANGUAGE "+hostRuntimeLanguage(b)+clause+" "+options+" AS "+transformsLiteral(transformsSource(b,program,python)))
}
var transformsMapRows = [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}}
var transformsDefaultRows = [][]any{{"a", "7"}, {"b", nil}, {"default", "entries"}}

func transformsHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
 t.Helper()
 stmt, err := c.PrepareContext(p.Ctx, q)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func() { stmt.Close() })
 return fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}
}
func transformsAssociation(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name string, count int64) {
 t.Helper()
 if count == 0 {
  hostRows(t,p,b,c,"SELECT protrftypes IS NULL AS v FROM pg_proc WHERE proname=$1",[]string{"v"},[]string{"BOOL"},[][]any{{true}},name)
  return
 }
 hostRows(t,p,b,c,"SELECT cardinality(protrftypes) AS n,protrftypes[1]='hstore'::regtype::oid AS v FROM pg_proc WHERE proname=$1",[]string{"n","v"},[]string{"INT4","BOOL"},[][]any{{count,true}},name)
 if count>1 {
  hostRows(t,p,b,c,"SELECT protrftypes[2]=protrftypes[1] AS v FROM pg_proc WHERE proname=$1",[]string{"v"},[]string{"BOOL"},[][]any{{true}},name)
 }
}
func transformsPairEdges(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,name string,want int64) {
 t.Helper()
 hostRows(t,p,b,c,"SELECT count(*) AS v FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid JOIN pg_transform t ON t.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_transform'::regclass AND d.deptype='n' AND p.proname=$1",[]string{"v"},[]string{"INT8"},[][]any{{want}},name)
}

// Complete diagnostics and protocol phases were captured on the pinned 18.6
// oracle in tests-1.md. Positions follow the same offending token when native
// language/source spellings change the statement's preceding byte length.
func transformsDiagnostic(t *testing.T,p *fixture.Pair,c *sql.Conn,q,code,message string) {
 t.Helper()
 want:=fixture.ControlsReferenceDiagnostic(code,message)
 preparedPhase:="execute"
 switch message {
 case "conflicting or redundant options":
  want.Position=strconv.Itoa(strings.LastIndex(q,"TRANSFORM")+1)
 case "syntax error at or near \"TRANSFORM\"":
  want.Position=strconv.Itoa(strings.Index(q,"TRANSFORM")+1)
  preparedPhase="prepare"
 case "syntax error at or near \"trailing\"":
  want.Position=strconv.Itoa(strings.LastIndex(q,"trailing")+1)
  preparedPhase="prepare"
 }
 for _, prepared := range []bool{false,true} {
  phase := "execute"
  var err error
  if prepared {
   stmt,e := c.PrepareContext(p.Ctx,q)
   if e != nil { err=e; phase="prepare" } else { _,err=stmt.ExecContext(p.Ctx); stmt.Close() }
  } else { _,err=c.ExecContext(p.Ctx,q) }
  if err == nil { t.Errorf("expected %s for %s",code,q); continue }
  got:=fixture.ExtractDiagnostic(t,err)
  wantPhase:="execute"
  if prepared {wantPhase=preparedPhase}
  if phase!=wantPhase {t.Errorf("%q prepared=%v phase=%s want %s",q,prepared,phase,wantPhase)}
  if got!=want {t.Errorf("%q prepared=%v diagnostic=%#v; want complete=%#v",q,prepared,got,want)}
 }
}
