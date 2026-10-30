package reference_test

import (
 "context"
 "errors"
 "runtime"
 "stuntgres/reference/fixture"
)

func attributeStatsBlockedCancellation(s attributeStatsScenario){
 s.t.Helper()
 holder,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer holder.Close()
 waiter,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer waiter.Close()
 if _,err=holder.ExecContext(s.p.Ctx,"BEGIN;LOCK TABLE ast_t IN ACCESS EXCLUSIVE MODE");err!=nil{s.t.Fatal(err)}
 defer holder.ExecContext(context.Background(),"ROLLBACK")
 ctx,cancel:=context.WithCancel(s.p.Ctx);defer cancel()
 q:=attributeStatsRestore("a",false,"'null_frac',.5::real")
 var invoke func()error
 if s.prepared{stmt,err:=waiter.PrepareContext(s.p.Ctx,q);if err!=nil{s.t.Fatal(err)};defer stmt.Close();invoke=func()error{var restored bool;return stmt.QueryRowContext(ctx).Scan(&restored)}}else{invoke=func()error{var restored bool;return waiter.QueryRowContext(ctx,q).Scan(&restored)}}
 result:=make(chan error,1);go func(){result<-invoke()}()
 joined:=false
 defer func(){cancel();if !joined{<-result}}()
 for {
  select{case err:=<-result:joined=true;s.t.Fatalf("restoration did not block behind relation lock: %v",err);default:}
  var waiting int
  err=s.db.QueryRowContext(s.p.Ctx,"SELECT count(*) FROM pg_locks WHERE relation='ast_t'::regclass AND mode='ShareUpdateExclusiveLock' AND NOT granted").Scan(&waiting)
  if err!=nil{s.t.Fatal(err)};if waiting==1{break};if err=s.p.Ctx.Err();err!=nil{s.t.Fatal(err)};runtime.Gosched()
 }
 cancel();err=<-result;joined=true
 if !errors.Is(err,context.Canceled){
  d:=fixture.ExtractDiagnostic(s.t,err)
  if d.Code!="57014"||d.Message!="canceling statement due to user request"{s.t.Fatalf("canceled lock wait: %#v",d)}
 }
 if _,err=holder.ExecContext(s.p.Ctx,"ROLLBACK");err!=nil{s.t.Fatal(err)}
 s.scalars([]any{"a",false,float64(.75),int64(0),float64(0)})
}
