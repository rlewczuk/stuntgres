package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "io"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func definitionOpen(t *testing.T,r *transformRuntime,mode string)(*sql.DB,fixture.Backend) {
 t.Helper()
 driver:=stuntgres.NewDriver()
 d:=definitionProvider(r)
 if mode=="abi" { d.ABIVersion="incompatible-definition" }
 if mode!="absent" { if err:=driver.RegisterHostLanguage(d); err!=nil { t.Fatal(err) } }
 connector,err:=driver.OpenConnector("definition-destination"); if err!=nil { t.Fatal(err) }
 return hostNativePools(t,connector)
}
func definitionPersistentSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 definitionAnnotate(t,p,b,c)
 hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
 definitionFunction(t,p,b,c,"td_persist","selection-v1",transformsSelectionPython)
 hostExec(t,p,b,c,"CREATE FUNCTION td_zero() RETURNS hstore LANGUAGE SQL AS $$ SELECT td_persist('a=>7,b=>NULL'::hstore) $$")
 hostExec(t,p,b,c,"CREATE TABLE td_persist_data(v integer)")
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_persist_data VALUES (11)",1))
 hostExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION td_persist(hstore) FROM PUBLIC")
}
func definitionPersistentResults(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,scaled bool) {
 t.Helper()
 from,to:="td_a_from","td_b_to"
 want:=transformsMapRows
 if scaled { from,to="td_scaled_from","td_projected_to"; want=[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}} }
 oid:=definitionPair(t,p,b,c,from,to); definitionEdges(t,p,b,c,oid,from,to)
 hostBag(t,p,b,c,"td_zero()",want)
 hostBag(t,p,b,c,"td_persist('a=>7,b=>NULL'::hstore)",want)
 hostRows(t,p,b,c,"SELECT v FROM td_persist_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
 hostRows(t,p,b,c,"SELECT proacl IS NOT NULL AS acl FROM pg_proc WHERE proname='td_persist'",[]string{"acl"},[]string{"BOOL"},[][]any{{true}})
}

func TestReferenceRoutineTransformDefinitionPersistence(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionPersistentSetup(t,p,b,c)
  definitionPersistentResults(t,p,b,c,false)
  if b.PG { return } // Public logical controls have no PostgreSQL counterpart.
  hostExec(t,p,b,c,definitionCreate("td_scaled_from","td_projected_to",true))
  hostExec(t,p,b,c,"ALTER FUNCTION td_scaled_from(internal) STABLE")
  // Preserve baseline bootstrap absence separately from the surviving alias.
  hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE go_host")
  held:=transformsHeld(t,p,c,"SELECT key,value FROM each(td_zero()) ORDER BY key")
  stream:=transformsDump(t,p.Ctx,p.Native)
  for round:=range 2 {
   fresh:=new(transformRuntime)
   db,target:=definitionOpen(t,fresh,"")
   if err:=stuntgres.Restore(p.Ctx,db,bytes.NewReader(stream)); err!=nil { t.Fatalf("fresh restore %d: %v",round,err) }
   definitionPersistentResults(t,p,target,target.DB,true)
   if fresh.body.Load()==0 { t.Fatal("destination's independently registered program was not executed") }
   hostRows(t,p,target,target.DB,"SELECT provolatile::text AS v FROM pg_proc WHERE proname='td_scaled_from'",[]string{"v"},[]string{"TEXT"},[][]any{{"s"}})
   hostRows(t,p,target,target.DB,"SELECT provolatile::text AS v FROM pg_proc WHERE proname='td_projected_to'",[]string{"v"},[]string{"TEXT"},[][]any{{"i"}})
   hostRows(t,p,target,target.DB,"SELECT provolatile::text AS v FROM pg_proc WHERE proname='td_scaled_to'",[]string{"v"},[]string{"TEXT"},[][]any{{"v"}})
   hostRows(t,p,target,target.DB,"SELECT count(*) AS n FROM pg_transform t JOIN pg_language l ON t.trflang=l.oid WHERE l.lanname='go_host' AND t.trftype='hstore'::regtype",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  }
  if err:=stuntgres.Reset(p.Ctx,p.Native); err!=nil { t.Fatal(err) }
  if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(stream)); err!=nil { t.Fatal(err) }
  definitionPersistentResults(t,p,b,c,true)
  hostRows(t,p,b,held,held.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}})
 })
}

func TestReferenceRoutineTransformDefinitionRestoreAtomicity(t *testing.T) {
 for _,mode:=range []string{"absent","abi","late-transform","reader","canceled","writer"} {
  t.Run(mode,func(t *testing.T) {
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionPersistentSetup(t,p,b,c)
    definitionPersistentResults(t,p,b,c,false)
    if b.PG { return }
    stream:=transformsDump(t,p.Ctx,p.Native)
    if mode=="writer" {
     if err:=stuntgres.Dump(p.Ctx,p.Native,transformsBrokenWriter{}); !errors.Is(err,io.ErrClosedPipe) { t.Fatalf("writer error=%v",err) }
     if after:=transformsDump(t,p.Ctx,p.Native); !bytes.Equal(stream,after) { t.Fatal("failed writer changed source state") }
     return
    }
    db,target:=definitionOpen(t,new(transformRuntime),mode)
    hostExec(t,p,target,target.DB,"CREATE TABLE td_old(v integer)")
    fixture.RunStep(t,p.Ctx,target.DB,target.Recorder,fixture.Exec("INSERT INTO td_old VALUES (99)",1))
    c,err:=target.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer c.Close()
    held:=transformsHeld(t,p,c,"SELECT v FROM td_old")
    hostExec(t,p,target,c,"CREATE EXTENSION hstore")
    hostExec(t,p,target,c,"CREATE FUNCTION td_old_zero() RETURNS hstore LANGUAGE SQL AS $$ SELECT 'old=>99,z=>kept'::hstore $$")
    zero:=transformsHeld(t,p,c,"SELECT key,value FROM each(td_old_zero()) ORDER BY key")
    hostRows(t,p,target,zero,zero.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"old","99"},{"z","kept"}})
    var nativeHeld fixture.HeldStatement
    if mode!="absent" {
     hostExec(t,p,target,c,"CREATE FUNCTION td_old_host(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'selection-v1'")
     nativeHeld=transformsHeld(t,p,c,"SELECT key,value FROM each(td_old_host('a=>7,b=>NULL'::hstore)) ORDER BY key")
     hostRows(t,p,target,nativeHeld,nativeHeld.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows)
    }
    retained,err:=db.QueryContext(p.Ctx,zero.SQL);if err!=nil {t.Fatal(err)};defer retained.Close()
    if !retained.Next() {t.Fatal("missing retained pre-failure row")};var key,value string
    if err=retained.Scan(&key,&value);err!=nil {t.Fatal(err)}
    if key!="old" || value!="99" {t.Fatal("unexpected retained pre-failure row",key,value)}
    before:=transformsDump(t,p.Ctx,db)
    data:=stream
    if mode=="late-transform" {
     data=append(append([]byte{},stream...),[]byte("\nCREATE OR REPLACE TRANSFORM FOR hstore LANGUAGE td_lang (TO SQL WITH FUNCTION td_a_from(internal));\n")...)
    }
    var reader io.Reader=bytes.NewReader(data)
    if mode=="reader" { reader=transformsBrokenReader{bytes.NewReader(data)} }
    ctx:=p.Ctx
    if mode=="canceled" { var cancel context.CancelFunc; ctx,cancel=context.WithCancel(ctx); cancel() }
    err=stuntgres.Restore(ctx,db,reader)
    if err==nil { t.Fatal("invalid/canceled restore published") }
    if mode=="late-transform" {
     // Exact late diagnostic proves the manifest and every prior real replay
     // statement were admitted, rather than accepting any preamble rejection.
     want:=fixture.ControlsReferenceDiagnostic("42P17","return data type of TO SQL function must be the transform data type")
     fixture.AssertDiagnostic(t,err,&want)
    }
    if mode=="canceled" && !errors.Is(err,context.Canceled) { t.Errorf("pre-canceled restore=%v",err) }
    hostRows(t,p,target,held,held.SQL,[]string{"v"},[]string{"INT4"},[][]any{{int64(99)}})
    if after:=transformsDump(t,p.Ctx,db); !bytes.Equal(before,after) { t.Fatal("failed restore changed populated destination") }
    hostRows(t,p,target,zero,zero.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"old","99"},{"z","kept"}})
    if nativeHeld.Statement!=nil {hostRows(t,p,target,nativeHeld,nativeHeld.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows)}
    if !retained.Next() {t.Fatal("failed restore revoked retained result continuation")}
    if err=retained.Scan(&key,&value);err!=nil {t.Fatal(err)}
    if key!="z" || value!="kept" {t.Fatal("failed restore changed retained result backing",key,value)}
    if retained.Next() {t.Error("retained result grew after failed restore")};if err=retained.Err();err!=nil {t.Error(err)};retained.Close()
   })
  })
 }
}

func TestReferenceRoutineTransformDefinitionReplayVolatility(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionPersistentSetup(t,p,b,c)
  from,to:="td_a_from","td_b_to"
  if !b.PG { from,to="td_scaled_from","td_projected_to" }
  for _,name:=range []string{from,to} { hostExec(t,p,b,c,"ALTER FUNCTION "+name+"(internal) STABLE") }
  hostExec(t,p,b,c,definitionCreate(from,to,true))
  oid:=definitionPair(t,p,b,c,from,to)
  for _,name:=range []string{from,to} { hostExec(t,p,b,c,"ALTER FUNCTION "+name+"(internal) VOLATILE") }
  definitionDiagnostic(t,p,c,definitionCreate(from,to,true),fixture.ControlsReferenceDiagnostic("42P17","transform function must not be volatile"),"execute")
  if current:=definitionPair(t,p,b,c,from,to); current!=oid { t.Fatal("rejected final-v replacement changed pair OID") }
  definitionPersistentResults(t,p,b,c,!b.PG)
  if b.PG { return }
  hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE go_host")
  held:=transformsHeld(t,p,c,"SELECT key,value FROM each(td_zero()) ORDER BY key")
  stream:=transformsDump(t,p.Ctx,p.Native)
  assert:=func(target fixture.Backend,executor fixture.Executor) {
   definitionPersistentResults(t,p,target,executor,true)
   for _,name:=range []string{from,to,"td_scaled_to"} {
    hostRows(t,p,target,executor,"SELECT provolatile::text AS v FROM pg_proc WHERE proname=$1",[]string{"v"},[]string{"TEXT"},[][]any{{"v"}},name)
   }
   hostRows(t,p,target,executor,"SELECT count(*) AS n FROM pg_transform t JOIN pg_language l ON t.trflang=l.oid WHERE l.lanname='go_host'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  }
  for range 2 {
   fresh:=new(transformRuntime); db,target:=definitionOpen(t,fresh,"")
   if err:=stuntgres.Restore(p.Ctx,db,bytes.NewReader(stream)); err!=nil { t.Fatal(err) }
   assert(target,target.DB)
   if fresh.body.Load()==0 { t.Fatal("final-v replay did not execute destination's genuine program") }
  }
  if err:=stuntgres.Reset(p.Ctx,p.Native); err!=nil { t.Fatal(err) }
  if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(stream)); err!=nil { t.Fatal(err) }
  assert(b,c)
  hostRows(t,p,b,held,held.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{{"a","71"},{"b",nil},{"c","3"},{"z","out"}})
 })
}
