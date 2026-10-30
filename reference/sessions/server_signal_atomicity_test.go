package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "testing"
 "stuntgres/reference/fixture"
)
func signalAtomicity(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("BEGIN",0));run(fixture.Exec("SAVEPOINT signal_point",0))
 run(fixture.Select("SELECT pg_log_backend_memory_contexts(-1) AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}));signalNotices(t,n,signalWarning("-1"))
 run(signalNull("SELECT pg_log_backend_memory_contexts(NULL) AS v"))
 if !b.PG{run(signalPhysical("SELECT pg_log_backend_memory_contexts(pg_backend_pid())",2,false));run(signalError("SELECT 7","25P02","current transaction is aborted, commands ignored until end of transaction block"));run(fixture.Exec("ROLLBACK TO SAVEPOINT signal_point",0))}
 run(fixture.Select("SELECT v FROM signal_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}));run(fixture.Exec("ROLLBACK",0))
 for _,prepared:=range []bool{false,true}{s:=fixture.Exec("SELECT pg_log_backend_memory_contexts(NULL)",1);s.Prepared=prepared;run(s);s=fixture.Exec("SELECT pg_reload_conf() WHERE FALSE",0);s.Prepared=prepared;run(s)}
 // Pre-canceled native calls have an ordinary context/no-output contract.
 // Fast pq commands may return outputs and dispose their physical owner;
 // do not cancel the shared oracle connection or assert native-only parity.
 if !b.PG {
  canceled,cancel:=context.WithCancel(p.Ctx);cancel()
  for _,q:=range []string{"SELECT pg_reload_conf()","SELECT pg_rotate_logfile()","SELECT pg_log_backend_memory_contexts(-1)"}{rows,err:=c.QueryContext(canceled,q);if rows!=nil{rows.Close();t.Fatal("canceled query returned cursor")};if !errors.Is(err,context.Canceled){t.Fatal("ordinary canceled caller",err)}}
 }
 signalNotices(t,n)
 run(fixture.Select("SELECT v FROM signal_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
}
