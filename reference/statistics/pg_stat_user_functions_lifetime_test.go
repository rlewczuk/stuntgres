package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserFunctionsLifetime(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"uf_moved")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"uf_lifetime","RETURN 7;");fixture.StatisticsExec(t,p,b,c,"CREATE SCHEMA uf_moved",0);userFunctionPublish(t,p,b,"uf_lifetime",1,1)
		held:=fixture.IdentityHeld(t,p.Ctx,c,userFunctionSQL);defer held.Statement.Close();old:=userFunctionGet(t,p,held,"uf_lifetime")
		fixture.StatisticsExec(t,p,b,c,"BEGIN",0);fixture.StatisticsExec(t,p,b,c,"ALTER FUNCTION uf_lifetime() RENAME TO uf_renamed",0);fixture.StatisticsExec(t,p,b,c,"ALTER FUNCTION uf_renamed() SET SCHEMA uf_moved",0)
		moved:=userFunctionGet(t,p,held,"uf_renamed");if moved.ID!=old.ID||moved.Schema!="uf_moved"||moved.Calls!=1{t.Fatal("current renamed identity",moved,old)};userFunctionAbsent(t,p,b,c,"uf_lifetime")
		fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0);if got:=userFunctionGet(t,p,held,"uf_lifetime");got!=old{t.Fatal("rollback names",got,old)}
		fixture.StatisticsExec(t,p,b,c,"CREATE OR REPLACE FUNCTION uf_lifetime() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 8; END$$",0)
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"uf_lifetime",8);fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_lifetime",2);fixture.StatisticsClearSnapshot(t,p,b,c)
		replaced:=userFunctionGet(t,p,held,"uf_lifetime");xactFunctionCounts(t,b,replaced,2,2,2);if replaced.ID!=old.ID{t.Fatal("replacement reminted identity",replaced,old)}
		fixture.StatisticsExec(t,p,b,c,"BEGIN",0);fixture.StatisticsExec(t,p,b,c,"DROP FUNCTION uf_lifetime()",0);userFunctionAbsent(t,p,b,c,"uf_lifetime");fixture.StatisticsExec(t,p,b,c,"ROLLBACK",0);if got:=userFunctionGet(t,p,held,"uf_lifetime");got!=replaced{t.Fatal("drop rollback lost facts",got,replaced)}
		fixture.StatisticsExec(t,p,b,c,"DROP FUNCTION uf_lifetime()",0);userFunctionAbsent(t,p,b,held,"uf_lifetime")
		fixture.StatisticsExec(t,p,b,c,"CREATE FUNCTION uf_lifetime() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$",0);userFunctionAbsent(t,p,b,held,"uf_lifetime");userFunctionPublish(t,p,b,"uf_lifetime",1,1);fixture.StatisticsClearSnapshot(t,p,b,c)
		fresh:=userFunctionGet(t,p,held,"uf_lifetime");xactFunctionCounts(t,b,fresh,1,1,1);if fresh.ID==old.ID{t.Fatal("recreated identity inherited statistics",fresh,old)};if old.Name!="uf_lifetime"||old.Schema!="public"||old.Calls!=1{t.Fatal("detached row mutated",old)}
		if !b.PG{
			fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=snapshot",0);fixture.StatisticsClearSnapshot(t,p,b,c)
			cancelled,cancel:=context.WithCancel(p.Ctx);cancel();expired,expire:=context.WithDeadline(p.Ctx,time.Unix(0,0));defer expire()
			for _,probe:=range []struct{ctx context.Context;want error}{{cancelled,context.Canceled},{expired,context.DeadlineExceeded}}{
				rows,err:=c.QueryContext(probe.ctx,userFunctionSQL,"uf_lifetime");if rows!=nil{rows.Close()};if !errors.Is(err,probe.want){t.Fatal("terminal query admission",err)};var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("fabricated SQLSTATE",err)}
				stmt,err:=c.PrepareContext(probe.ctx,userFunctionSQL);if stmt!=nil{stmt.Close()};if !errors.Is(err,probe.want)||errors.As(err,&state){t.Fatal("terminal prepare admission",err)}
				var at sql.NullTime;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_stat_get_snapshot_timestamp()").Scan(&at);err!=nil||at.Valid{t.Fatal("terminal admission created cut",at,err)}
			}
			if got:=userFunctionGet(t,p,held,"uf_lifetime");got!=fresh{t.Fatal("terminal admission damaged continuation",got,fresh)}
		}
	})
}
