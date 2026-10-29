package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

func TestReferencePartitionRenameColumnLocks(t *testing.T){
 for _,mode:=range []string{"timeout","cancel","complete","authority_after_closure"}{for _,prepared:=range []bool{false,true}{
  name:=mode+"/direct";if prepared{name=mode+"/prepared"};t.Run(name,func(t *testing.T){
   if mode=="authority_after_closure"{fixture.RoleReferenceCleanup(t,[]string{"pr_owner","pr_other"})}
   referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
    ctx,cancel:=context.WithTimeout(p.Ctx,10*time.Second);defer cancel()
    run:=func(s fixture.Step){fixture.RunStep(t,ctx,b.DB,b.Recorder,s)};partitionRenameSetup(run)
    worker,e:=b.DB.Conn(ctx);if e!=nil{t.Fatal(e)};defer worker.Close()
    if mode=="authority_after_closure"{
     run(fixture.Exec("CREATE ROLE pr_owner LOGIN",0));run(fixture.Exec("CREATE ROLE pr_other LOGIN",0));run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pr_owner",0))
     for _,table:=range []string{"pr_root","pr_mid","pr_hash","pr_h0","pr_h1"}{run(fixture.Exec("ALTER TABLE "+table+" OWNER TO pr_owner",0))};run(fixture.Exec("ALTER TABLE pr_mid OWNER TO pr_other",0))
     fixture.RunStep(t,ctx,worker,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION pr_owner",0))
    }
    q:="ALTER TABLE pr_root RENAME k TO renamed";var stmt *sql.Stmt
    if prepared{stmt,e=worker.PrepareContext(ctx,q);if e!=nil{t.Fatal(e)};defer stmt.Close()}
    holder,e:=b.DB.BeginTx(ctx,nil);if e!=nil{t.Fatal(e)};defer holder.Rollback()
    fixture.RunStep(t,ctx,holder,b.Recorder,fixture.Select("SELECT k FROM pr_h1",[]string{"k"},[]string{"INT4"},[][]any{{int64(3)}}))
    call:=func(callCtx context.Context)(sql.Result,error){if stmt!=nil{return stmt.ExecContext(callCtx)};return worker.ExecContext(callCtx,q)}
    if mode=="timeout"||mode=="authority_after_closure"{
     fixture.RunStep(t,ctx,worker,b.Recorder,fixture.Exec("SET lock_timeout='10ms'",0))
     result,err:=call(ctx);if result!=nil{t.Fatal("failed rename returned result")};fixture.AssertDiagnostic(t,err,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"55P03",Message:"canceling statement due to lock timeout"})
     // Dispose of the armed borrower rather than executing RESET at its timeout.
     if stmt!=nil{stmt.Close()};worker.Close();if e:=holder.Rollback();e!=nil{t.Fatal(e)}
     partitionRenameRows(run,"k","payload");run(fixture.Exec(q,0));partitionRenameRows(run,"renamed","payload");return
    }
    workerPID,holderPID:=0,0
    if b.PG{workerPID=fixture.LockPID(t,ctx,worker);if e:=holder.QueryRowContext(ctx,"SELECT pg_backend_pid()").Scan(&holderPID);e!=nil{t.Fatal(e)}}
    wc,stop:=context.WithCancel(ctx);defer stop()
    type outcome struct{r sql.Result;e error};done:=make(chan outcome,1);joined:=false
    defer func(){stop();holder.Rollback();if !joined{select{case <-done:case <-time.After(2*time.Second):t.Error("rename worker not joined")}}}()
    go func(){r,e:=call(wc);done<-outcome{r,e}}()
    if b.PG{fixture.LockWaitBehind(t,ctx,workerPID,holderPID,fixture.LockOID(t,ctx,"pr_h1"),"AccessExclusiveLock")}else{
     probe,e:=b.DB.Conn(ctx);if e!=nil{t.Fatal(e)};defer probe.Close();fixture.RunStep(t,ctx,probe,b.Recorder,fixture.Exec("SET lock_timeout='10ms'",0))
     for{
      select{case got:=<-done:joined=true;t.Fatal("rename completed before deep lock admission",got.e);default:}
      rows,err:=probe.QueryContext(ctx,"SELECT k FROM pr_h1");if rows!=nil{for rows.Next(){};rows.Close()}
      if err!=nil{fixture.AssertDiagnostic(t,err,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"55P03",Message:"canceling statement due to lock timeout",Position:referenceRangePosition("SELECT k FROM pr_h1","pr_h1")});break}
      if ctx.Err()!=nil{t.Fatal(ctx.Err())}
     }
    }
    // A final BFS member blocks; every earlier member must already carry AX.
    for _,table:=range []string{"pr_mid","pr_hash","pr_h0"}{
     probe,e:=b.DB.Conn(ctx);if e!=nil{t.Fatal(e)}
     fixture.RunStep(t,ctx,probe,b.Recorder,fixture.Exec("SET lock_timeout='10ms'",0))
     s:=referenceRangeError("SELECT k FROM ONLY "+table,"55P03","canceling statement due to lock timeout",fixture.Diagnostic{Position:referenceRangePosition("SELECT k FROM ONLY "+table,table)});s.SelectRows=true
     fixture.RunStep(t,ctx,probe,b.Recorder,s);probe.Close()
    }
    if mode=="cancel"{if b.PG{var ok bool;if e:=fixture.Admin.QueryRowContext(ctx,"SELECT pg_cancel_backend($1)",workerPID).Scan(&ok);e!=nil||!ok{t.Fatal(ok,e)}}else{stop()}}else{if e:=holder.Rollback();e!=nil{t.Fatal(e)}}
    select{case got:=<-done:joined=true
     if mode=="cancel"{if got.r!=nil{t.Fatal("canceled rename result")};if b.PG{fixture.AssertDiagnostic(t,got.e,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"57014",Message:"canceling statement due to user request"})}else if !errors.Is(got.e,context.Canceled){t.Fatal(got.e)}}else{if got.e!=nil{t.Fatal(got.e)};n,e:=got.r.RowsAffected();if e!=nil||n!=0{t.Fatal(n,e)}}
    case <-ctx.Done():t.Fatal(ctx.Err())}
    if mode=="cancel"{holder.Rollback();partitionRenameRows(run,"k","payload");run(fixture.Exec(q,0))};partitionRenameRows(run,"renamed","payload")
   })
  })
 }}
}
