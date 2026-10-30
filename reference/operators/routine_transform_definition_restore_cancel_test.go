package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionActiveRestore(t *testing.T) {
 definitionScenarioRuntime(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
  definitionPersistentSetup(t,p,b,c)
  program:="selection-v1"
  if !b.PG {program="wait-v1";r.mu.Lock();r.gate=func(context.Context)error{return nil};r.mu.Unlock();defer func(){r.mu.Lock();r.gate=nil;r.mu.Unlock()}()}
  definitionFunction(t,p,b,c,"td_replay_gate",program,transformsSelectionPython)
  hostExec(t,p,b,c,"CREATE TABLE td_replay_data(v hstore CHECK(td_replay_gate(v) IS NOT NULL))")
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_replay_data VALUES('a=>7,b=>NULL'::hstore)",1))
  hostBag(t,p,b,c,"(SELECT v FROM td_replay_data)",[][]any{{"a","7"},{"b",nil}})
  definitionPersistentResults(t,p,b,c,false)
  if b.PG {return} // Replay is native; above uses the real released Python CHECK.
  stream:=transformsDump(t,p.Ctx,p.Native)
  native:=new(transformRuntime);db,target:=definitionOpen(t,native,"")
  conn,err:=target.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer conn.Close()
  hostExec(t,p,target,conn,"CREATE EXTENSION hstore")
  hostExec(t,p,target,conn,"CREATE TABLE td_replay_old(v integer)")
  fixture.RunStep(t,p.Ctx,conn,target.Recorder,fixture.Exec("INSERT INTO td_replay_old VALUES(99)",1))
  hostExec(t,p,target,conn,"CREATE FUNCTION td_replay_old_host(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'selection-v1'")
  held:=transformsHeld(t,p,conn,"SELECT key,value FROM each(td_replay_old_host('a=>7,b=>NULL'::hstore)) ORDER BY key")
  hostRows(t,p,target,held,held.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows)
  retained,err:=db.QueryContext(p.Ctx,held.SQL);if err!=nil {t.Fatal(err)};defer retained.Close()
  if !retained.Next(){t.Fatal("missing actual pre-restore result")};var key string;var value sql.NullString
  if err=retained.Scan(&key,&value);err!=nil{t.Fatal(err)};if key!="a" || !value.Valid || value.String!="8" {t.Fatal("invalid retained result")}
  before:=transformsDump(t,p.Ctx,db)
  entered:=make(chan struct{});native.mu.Lock();native.gate=func(ctx context.Context)error{close(entered);<-ctx.Done();return ctx.Err()};native.mu.Unlock()
  active,cancel:=context.WithCancel(p.Ctx);done:=make(chan error,1);joined:=false
  defer func(){cancel();if !joined {<-done};native.mu.Lock();native.gate=nil;native.mu.Unlock()}()
  go func(){done<-stuntgres.Restore(active,db,bytes.NewReader(stream))}()
  select {
  case <-entered:
   // Reaching this genuine CHECK program proves registration, type/callback,
   // language, explicit pair and consumer declarations really staged first.
  case err=<-done:
   joined=true;t.Fatalf("restore failed before actual staged CHECK callback: %v",err)
  case <-p.Ctx.Done():t.Fatal(p.Ctx.Err())
  }
  cancel();err=<-done;joined=true
  if !errors.Is(err,context.Canceled){t.Fatalf("active replay cancel=%v",err)}
  native.mu.Lock();native.gate=nil;native.mu.Unlock()
  if after:=transformsDump(t,p.Ctx,db);!bytes.Equal(before,after){t.Fatal("active replay cancellation changed populated target")}
  hostRows(t,p,target,conn,"SELECT v FROM td_replay_old",[]string{"v"},[]string{"INT4"},[][]any{{int64(99)}})
  hostRows(t,p,target,held,held.SQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows)
  for _,expected:=range []struct{key string;value sql.NullString}{{"b",sql.NullString{}},{"c",sql.NullString{String:"3",Valid:true}}} {
   if !retained.Next(){t.Fatal("canceled replay revoked retained result continuation")}
   if err=retained.Scan(&key,&value);err!=nil{t.Fatal(err)};if key!=expected.key || value!=expected.value {t.Fatal("canceled replay changed retained backing",key,value)}
  }
  if retained.Next(){t.Error("retained result gained rows")};if err=retained.Err();err!=nil{t.Error(err)};retained.Close()
  // A fresh, later success must still execute this destination's supplied
  // program, not reuse a source token or inherit the canceled callback frame.
  native.mu.Lock();native.gate=func(context.Context)error{return nil};native.mu.Unlock()
  beforeRecovery:=native.body.Load()
  if err=stuntgres.Restore(p.Ctx,db,bytes.NewReader(stream));err!=nil{t.Fatal(err)}
  definitionPersistentResults(t,p,target,target.DB,false)
  if native.body.Load()<=beforeRecovery {t.Fatal("successful recovery did not execute destination program")}
 })
}
