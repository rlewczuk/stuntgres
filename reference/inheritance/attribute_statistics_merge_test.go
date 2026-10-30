package reference_test

import (
 "context"
 "errors"
 "runtime"
)

func attributeStatsDisjointMerge(s attributeStatsScenario){
 s.t.Helper()
 peer,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer peer.Close()
 defer s.conn.ExecContext(context.Background(),"ROLLBACK")
 defer peer.ExecContext(context.Background(),"ROLLBACK")
 if _,err=peer.ExecContext(s.p.Ctx,"BEGIN");err!=nil{s.t.Fatal(err)}
 var prior float64
 if err=peer.QueryRowContext(s.p.Ctx,"SELECT null_frac FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited").Scan(&prior);err!=nil||prior!=.75{s.t.Fatal(prior,err)}
 s.exec("BEGIN");s.restore("'null_frac',.5::real",true)
 ctx,cancel:=context.WithCancel(s.p.Ctx);defer cancel()
 result:=make(chan error,1)
 go func(){var restored bool;err:=peer.QueryRowContext(ctx,attributeStatsRestore("b",false,"'null_frac',.625::real")).Scan(&restored);if err==nil&&!restored{err=errors.New("disjoint restore returned false")};result<-err}()
 joined:=false;defer func(){cancel();if !joined{<-result}}()
 for{
  select{case err:=<-result:joined=true;s.t.Fatalf("disjoint transaction did not honor maintenance lock: %v",err);default:}
  var waiting int;if err=s.db.QueryRowContext(s.p.Ctx,"SELECT count(*) FROM pg_locks WHERE relation='ast_t'::regclass AND mode='ShareUpdateExclusiveLock' AND NOT granted").Scan(&waiting);err!=nil{s.t.Fatal(err)};if waiting==1{break};if err=s.p.Ctx.Err();err!=nil{s.t.Fatal(err)};runtime.Gosched()
 }
 s.exec("COMMIT");err=<-result;joined=true;if err!=nil{s.t.Fatal(err)}
 s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)})
 var own float64;if err=peer.QueryRowContext(s.p.Ctx,"SELECT null_frac FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='b' AND NOT inherited").Scan(&own);err!=nil||own!=.625{s.t.Fatal(own,err)}
 if _,err=peer.ExecContext(s.p.Ctx,"COMMIT");err!=nil{s.t.Fatal(err)}
 s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)},[]any{"b",false,float64(.625),int64(0),float64(0)})
}
