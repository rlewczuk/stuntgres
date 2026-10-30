package reference_test

import (
 "context"
 "errors"
 "runtime"
 "stuntgres/reference/fixture"
)

func relationStatsBlockedCancellation(s relationStatsScenario){
 s.t.Helper()
 holder,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer holder.Close()
 waiter,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer waiter.Close()
 if _,err=holder.ExecContext(s.p.Ctx,"BEGIN;LOCK TABLE rst_t IN ACCESS EXCLUSIVE MODE");err!=nil{s.t.Fatal(err)}
 defer holder.ExecContext(context.Background(),"ROLLBACK")
 ctx,cancel:=context.WithCancel(s.p.Ctx);defer cancel()
 q:=relationStatsRestore("rst_t","'reltuples',99::real")
 var invoke func()error
 if s.prepared{stmt,err:=waiter.PrepareContext(s.p.Ctx,q);if err!=nil{s.t.Fatal(err)};defer stmt.Close();invoke=func()error{var restored bool;return stmt.QueryRowContext(ctx).Scan(&restored)}}else{invoke=func()error{var restored bool;return waiter.QueryRowContext(ctx,q).Scan(&restored)}}
 result:=make(chan error,1);go func(){result<-invoke()}()
 joined:=false
 defer func(){cancel();if !joined{<-result}}()
 for {
  select{case err:=<-result:joined=true;s.t.Fatalf("restoration did not block behind relation lock: %v",err);default:}
  var waiting int
  err=s.db.QueryRowContext(s.p.Ctx,"SELECT count(*) FROM pg_locks WHERE relation='rst_t'::regclass AND mode='ShareUpdateExclusiveLock' AND NOT granted").Scan(&waiting)
  if err!=nil{s.t.Fatal(err)};if waiting==1{break};if err=s.p.Ctx.Err();err!=nil{s.t.Fatal(err)};runtime.Gosched()
 }
 cancel();err=<-result;joined=true
 if !errors.Is(err,context.Canceled){
  d:=fixture.ControlsReferenceDiagnostic("57014","canceling statement due to user request")
  fixture.AssertDiagnostic(s.t,err,&d)
 }
 if _,err=holder.ExecContext(s.p.Ctx,"ROLLBACK");err!=nil{s.t.Fatal(err)}
 s.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='rst_t'::regclass",[]string{"reltuples"},[]string{"FLOAT4"},[][]any{{float64(25.5)}}))
}

func relationStatsDisjointMerge(s relationStatsScenario){
 s.t.Helper()
 peer,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer peer.Close()
 defer s.conn.ExecContext(context.Background(),"ROLLBACK")
 defer peer.ExecContext(context.Background(),"ROLLBACK")
 s.exec("BEGIN");s.restore("'relpages',41",true)
 ctx,cancel:=context.WithCancel(s.p.Ctx);defer cancel()
 result:=make(chan error,1)
 go func(){var restored bool;err:=peer.QueryRowContext(ctx,relationStatsRestore("rst_t","'reltuples',42.5::real")).Scan(&restored);if err==nil&&!restored{err=errors.New("disjoint restore returned false")};result<-err}()
 joined:=false
 defer func(){cancel();if !joined{<-result}}()
 for {
  select{case err:=<-result:joined=true;s.t.Fatalf("disjoint update failed to honor maintenance lock: %v",err);default:}
  var waiting int
  if err=s.db.QueryRowContext(s.p.Ctx,"SELECT count(*) FROM pg_locks WHERE relation='rst_t'::regclass AND mode='ShareUpdateExclusiveLock' AND NOT granted").Scan(&waiting);err!=nil{s.t.Fatal(err)}
  if waiting==1{break};if err=s.p.Ctx.Err();err!=nil{s.t.Fatal(err)};runtime.Gosched()
 }
 s.exec("COMMIT");err=<-result;joined=true;if err!=nil{s.t.Fatal(err)}
 s.run(fixture.Select("SELECT relpages,reltuples FROM pg_class WHERE oid='rst_t'::regclass",[]string{"relpages","reltuples"},[]string{"INT4","FLOAT4"},[][]any{{int64(41),float64(42.5)}}))
}
