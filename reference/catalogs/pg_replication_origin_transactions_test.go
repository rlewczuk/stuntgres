package reference_test

import (
 "context"
 "fmt"
 "reflect"
 "stuntgres/reference/fixture"
 "testing"
 "time"
)

func TestReferenceReplicationOriginTransactions(t *testing.T) {
 p:=subscriptionScenario(t,"origin_tx","origin_suffix","origin_outer")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  actor,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
  defer actor.ExecContext(context.Background(),"ROLLBACK")
  initial:=originBag(t,p,observer)
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  originCreate(t,p,b,actor,"origin_tx",false)
  id,name:=originIdentity(t,p,actor,"origin_tx")
  originCheck(t,p,b,actor,"origin_tx",id,name); originCheck(t,p,b,observer,"origin_tx",0,"")
  subscriptionExec(t,p,b,actor,"ROLLBACK",false)
  if !reflect.DeepEqual(initial,originBag(t,p,observer)) { t.Fatal("rollback leaked origin") }
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  originCreate(t,p,b,actor,"origin_tx",true)
  id,name=originIdentity(t,p,actor,"origin_tx")
  subscriptionExec(t,p,b,actor,"COMMIT",false)
  originCheck(t,p,b,observer,"origin_tx",id,name)
  query:=fmt.Sprintf("SELECT * FROM pg_replication_origin WHERE roname='%s'",name)
  stmt,err:=observer.PrepareContext(p.Ctx,query); if err!=nil { t.Fatal(err) }; defer stmt.Close()
  held:=fixture.HeldStatement{Executor:observer,Statement:stmt,SQL:query}
  // The acquired native result owns its immutable typed row, unlike a held
  // statement which captures again on every execution.
  var oldID int64; var oldName string
  old,err:=b.DB.QueryContext(p.Ctx,query); if err!=nil { t.Fatal(err) }; defer old.Close()
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION origin_tx",true)
  originCheck(t,p,b,actor,"origin_tx",0,""); originCheck(t,p,b,observer,"origin_tx",id,name)
  subscriptionExec(t,p,b,actor,"ROLLBACK",false)
  originCheck(t,p,b,actor,"origin_tx",id,name)
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  subscriptionExec(t,p,b,actor,"SAVEPOINT retained",false)
  subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION origin_tx",false)
  subscriptionExec(t,p,b,actor,"ROLLBACK TO retained",false)
  originCheck(t,p,b,actor,"origin_tx",id,name)
  originCreate(t,p,b,actor,"origin_suffix",false)
  subscriptionExec(t,p,b,actor,"ROLLBACK TO retained",false)
  originCheck(t,p,b,actor,"origin_suffix",0,"")
  subscriptionError(t,p,b,actor,"DROP SUBSCRIPTION origin_missing","42704",`subscription "origin_missing" does not exist`,false)
  subscriptionExec(t,p,b,actor,"ROLLBACK TO retained",false)
  originCheck(t,p,b,actor,"origin_tx",id,name)
  subscriptionExec(t,p,b,actor,"RELEASE retained",false)
  subscriptionExec(t,p,b,actor,"COMMIT",false)
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  originCreate(t,p,b,actor,"origin_outer",false)
  subscriptionExec(t,p,b,actor,"SAVEPOINT suffix",false)
  originCreate(t,p,b,actor,"origin_suffix",false)
  subscriptionExec(t,p,b,actor,"RELEASE suffix",false)
  subscriptionExec(t,p,b,actor,"ROLLBACK",false)
  originCheck(t,p,b,observer,"origin_outer",0,""); originCheck(t,p,b,observer,"origin_suffix",0,"")
  subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION origin_tx",false)
  subscriptionRun(t,p,b,held,originEmpty(b.PG,query))
  if !old.Next() { t.Fatal("retained origin missing",old.Err()) }; if err:=old.Scan(&oldID,&oldName);err!=nil { t.Fatal(err) }
  if oldID!=id || oldName!=name || old.Next() || old.Err()!=nil { t.Fatal("retained typed origin changed",oldID,oldName,old.Err()) }; old.Close()
  if !reflect.DeepEqual(initial,originBag(t,p,observer)) { t.Fatal("transaction origin orphan") }
 }) }
}

func TestReferenceReplicationOriginShared(t *testing.T) {
 databases:=[]string{"origin_shared_database","origin_later_database"}; fixture.DatabaseReferenceCleanup(t,&databases)
 p:=subscriptionScenario(t,"origin_shared","origin_released")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  subscriptionExec(t,p,b,b.DB,"CREATE DATABASE origin_shared_database",false)
  peer,_,recorder:=fixture.DatabaseScenarioPool(t,p,b.PG,"origin_shared_database","")
  pb:=b; pb.DB=peer; pb.Recorder=recorder
  t.Cleanup(func() { ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second); defer cancel(); for _,name:=range []string{"origin_shared","origin_released"} { for _,q:=range []string{"ALTER SUBSCRIPTION "+name+" DISABLE","ALTER SUBSCRIPTION "+name+" SET (slot_name=NONE)","DROP SUBSCRIPTION IF EXISTS "+name} { peer.ExecContext(ctx,q) } } })
  a,err:=b.DB.BeginTx(p.Ctx,nil); if err!=nil { t.Fatal(err) }; defer a.Rollback()
  c,err:=peer.BeginTx(p.Ctx,nil); if err!=nil { t.Fatal(err) }; defer c.Rollback()
  // Both complete before either commit: no transaction-long allocator lock.
  originCreate(t,p,b,a,"origin_shared",false)
  first,firstName:=originIdentity(t,p,a,"origin_shared")
  originCreate(t,p,pb,c,"origin_shared",true)
  second,secondName:=originIdentity(t,p,c,"origin_shared")
  if first==second || firstName==secondName { t.Fatal("pending registration collision") }
  originCheck(t,p,b,a,"origin_shared",first,firstName)
  originCheck(t,p,pb,c,"origin_shared",second,secondName)
  if err:=a.Commit();err!=nil { t.Fatal(err) }; if err:=c.Commit();err!=nil { t.Fatal(err) }
  left,right:=originBag(t,p,b.DB),originBag(t,p,peer)
  if !reflect.DeepEqual(left,right) || left[first]!=firstName || left[second]!=secondName { t.Fatal("shared publication",left,right) }
  subscriptionExec(t,p,b,b.DB,"CREATE DATABASE origin_later_database",false)
  later,_,_:=fixture.DatabaseScenarioPool(t,p,b.PG,"origin_later_database","")
  if !reflect.DeepEqual(left,originBag(t,p,later)) { t.Fatal("new database did not inherit exact shared registrations") }
  tx,err:=peer.BeginTx(p.Ctx,nil); if err!=nil { t.Fatal(err) }; defer tx.Rollback()
  free:=originFirstFree(left)
  originCreate(t,p,pb,tx,"origin_released",false)
  pending,_:=originIdentity(t,p,tx,"origin_released"); if pending!=free { t.Fatal("pending first free",pending,free) }
  if err:=tx.Rollback();err!=nil { t.Fatal(err) }
  originCreate(t,p,b,b.DB,"origin_released",false)
  reused,_:=originIdentity(t,p,b.DB,"origin_released"); if reused!=free { t.Fatal("rollback did not release first free",reused,free) }
  subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_released",false)
  subscriptionExec(t,p,b,b.DB,"DROP SUBSCRIPTION origin_shared",false)
  originCheck(t,p,pb,peer,"origin_shared",second,secondName)
  subscriptionExec(t,p,pb,peer,"DROP SUBSCRIPTION origin_shared",false)
 }) }
}
