package reference_test

import (
 "context"
 "database/sql"
 "runtime"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

func transformsCancellation(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
 t.Helper()
 transformsFunction(t,p,b,c,"tr_cancel","wait-v1","assert isinstance(v,dict)\nv['a']=str(int(v['a'])+1)\nv['c']='3'\nplpy.execute('SELECT pg_advisory_xact_lock(638127)')\nreturn v","FOR TYPE hstore","")
 hostExec(t,p,b,c,"CREATE FUNCTION tr_cancel_prefix() RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO tr_atomic_data VALUES (2); RETURN tr_cancel('a=>7,b=>NULL'::hstore); END $$")
 q:="SELECT tr_cancel_prefix()"
 held:=transformsHeld(t,p,c,q)
 for _,exec:=range []fixture.Executor{c,held}{
  if !b.PG{
   hostCancelAdmitted(t,p,&r.hostNativeRuntime,func(ctx context.Context)error{
    rows,err:=exec.QueryContext(ctx,q);if rows!=nil{rows.Close()};return err
   })
  }else{
   transformsPGCancel(t,p,b,c,exec,q)
  }
  hostRows(t,p,b,c,"SELECT v FROM tr_atomic_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)}})
 }
 transformsFunction(t,p,b,c,"tr_cancel","increment-v1",hostIncrementPython,"FOR TYPE hstore","")
 rows,err:=held.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
 if !rows.Next(){t.Fatal("missing recovered canceled CALL")};var raw string;if err=rows.Scan(&raw);err!=nil{t.Fatal(err)};if rows.Next(){t.Fatal("extra canceled recovery output")};if err=rows.Err();err!=nil{t.Fatal(err)};rows.Close()
 hostBag(t,p,b,c,"$1::hstore",transformsMapRows,raw)
 hostRows(t,p,b,c,"SELECT v FROM tr_atomic_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}})
}

func transformsPGCancel(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,exec fixture.Executor,q string){
 t.Helper()
 observer,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer observer.Close()
 ctx,cancel:=context.WithTimeout(p.Ctx,15*time.Second);defer cancel()
 var pid int
 if err=c.QueryRowContext(ctx,"SELECT pg_backend_pid()").Scan(&pid);err!=nil{t.Fatal(err)}
 if _,err=observer.ExecContext(ctx,"SELECT pg_advisory_lock(638127)");err!=nil{t.Fatal(err)}
 defer func(){cleanup,stop:=context.WithTimeout(context.Background(),5*time.Second);defer stop();if _,err:=observer.ExecContext(cleanup,"SELECT pg_advisory_unlock(638127)");err!=nil{t.Error(err)}}()
 done:=make(chan error,1);joined:=false
 defer func(){if !joined{cancel();select{case <-done:case <-time.After(5*time.Second):t.Error("transform cancel worker failed to join")}}}()
 go func(){rows,err:=exec.QueryContext(ctx,q);if rows!=nil{rows.Close()};done<-err}()
 for {
  var admitted bool
  if err=observer.QueryRowContext(ctx,"SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state='active' AND wait_event_type='Lock' AND wait_event='advisory')",pid).Scan(&admitted);err!=nil{t.Fatal(err)}
  if admitted{break}
  select{case err:=<-done:joined=true;t.Fatalf("transform returned before admitted cancellation: %v",err);default:}
  runtime.Gosched()
 }
 var canceled bool
 if err=observer.QueryRowContext(ctx,"SELECT pg_cancel_backend($1)",pid).Scan(&canceled);err!=nil||!canceled{t.Fatalf("cancel=%v/%v",canceled,err)}
 select{
 case err:=<-done:
  joined=true
  if err==nil{t.Fatal("admitted transform cancellation succeeded")}
  d:=fixture.ExtractDiagnostic(t,err)
  want:=fixture.ControlsReferenceDiagnostic("57014","spiexceptions.QueryCanceled: canceling statement due to user request")
  want.Context="Traceback (most recent call last):\n  PL/Python function \"tr_cancel\", line 4, in <module>\n    plpy.execute('SELECT pg_advisory_xact_lock(638127)')\nPL/Python function \"tr_cancel\"\nPL/pgSQL function tr_cancel_prefix() line 1 at RETURN"
  if d!=want {t.Errorf("admitted cancellation phase=execute diagnostic=%#v; want complete=%#v",d,want)}
 case <-ctx.Done():t.Fatal("admitted cancellation failed to unwind")
 }
}
