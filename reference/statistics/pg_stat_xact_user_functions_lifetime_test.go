package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserFunctionsLifetime(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"xf_moved")
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_lifetime","RETURN 7;");fixture.StatisticsExec(t,p,b,c,"CREATE SCHEMA xf_moved",0)
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"xf_lifetime",7)
		old:=xactFunctionGet(t,p,w,"xf_lifetime")
		fixture.StatisticsExec(t,p,b,w,"SAVEPOINT ddl",0)
		fixture.StatisticsExec(t,p,b,w,"ALTER FUNCTION xf_lifetime() RENAME TO xf_renamed",0);fixture.StatisticsExec(t,p,b,w,"ALTER FUNCTION xf_renamed() SET SCHEMA xf_moved",0)
		moved:=xactFunctionGet(t,p,w,"xf_renamed");if moved.ID!=old.ID||moved.Schema!="xf_moved"||moved.Calls!=1{t.Fatal("current identity",moved,old)};xactFunctionAbsent(t,p,b,w,"xf_lifetime")
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO ddl",0);fixture.StatisticsExec(t,p,b,w,"RELEASE ddl",0)
		restored:=xactFunctionGet(t,p,w,"xf_lifetime");if restored!=old{t.Fatal("DDL rollback",restored,old)}
		fixture.StatisticsExec(t,p,b,w,"CREATE OR REPLACE FUNCTION xf_lifetime() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 8; END$$",0)
		xactFunctionCall(t,p,b,w,"xf_lifetime",8);replacement:=xactFunctionGet(t,p,w,"xf_lifetime");xactFunctionCounts(t,b,replacement,2,2,2);if replacement.ID!=old.ID{t.Fatal("compatible replacement reminted",replacement,old)}
		fixture.StatisticsExec(t,p,b,w,"SAVEPOINT private",0);fixture.StatisticsExec(t,p,b,w,"CREATE FUNCTION xf_aborted() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$",0);xactFunctionCall(t,p,b,w,"xf_aborted",7)
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK TO private",0);fixture.StatisticsExec(t,p,b,w,"RELEASE private",0);xactFunctionAbsent(t,p,b,w,"xf_aborted")
		fixture.StatisticsExec(t,p,b,w,"DROP FUNCTION xf_lifetime()",0);xactFunctionAbsent(t,p,b,w,"xf_lifetime")
		fixture.StatisticsExec(t,p,b,w,"CREATE FUNCTION xf_lifetime() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 9; END$$",0);xactFunctionAbsent(t,p,b,w,"xf_lifetime");xactFunctionCall(t,p,b,w,"xf_lifetime",9)
		fresh:=xactFunctionGet(t,p,w,"xf_lifetime");xactFunctionCounts(t,b,fresh,1,1,1);if fresh.ID==old.ID{t.Fatal("recreated identity inherited",fresh,old)}
		if old.Name!="xf_lifetime"||old.Schema!="public"||old.Calls!=1{t.Fatal("retained values changed",old)}
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
	})
}

func TestReferencePGStatXactUserFunctionsCancellation(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xactFunctionCreate(t,p,b,c,"xf_cancel","RETURN 7;")
		w,retire:=fixture.StatisticsReportingConn(t,p,b);defer retire();xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"xf_cancel",7);old:=xactFunctionGet(t,p,w,"xf_cancel")
		if !b.PG {
			// Native adapter admission is independently testable. A fast pq
			// request has a transport race, not a portable server witness.
			cancelled,cancel:=context.WithCancel(p.Ctx);cancel()
			expired,expire:=context.WithDeadline(p.Ctx,time.Unix(0,0));defer expire()
			for _,probe:=range []struct{name string;ctx context.Context;want error}{
				{"canceled",cancelled,context.Canceled},
				{"expired",expired,context.DeadlineExceeded},
			}{
				if !errors.Is(probe.ctx.Err(),probe.want){t.Fatal("probe not already terminal",probe.name,probe.ctx.Err())}
				rows,err:=w.QueryContext(probe.ctx,xactFunctionSQL,"xf_cancel");if rows!=nil{rows.Close()}
				if !errors.Is(err,probe.want){t.Fatal("native adapter query",probe.name,err)}
				var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("fabricated SQLSTATE",probe.name,state.SQLState())}
				stmt,err:=w.PrepareContext(probe.ctx,xactFunctionSQL);if stmt!=nil{stmt.Close()}
				if !errors.Is(err,probe.want){t.Fatal("native adapter prepare",probe.name,err)}
				if errors.As(err,&state){t.Fatal("fabricated prepare SQLSTATE",probe.name,state.SQLState())}
				if got:=xactFunctionGet(t,p,w,"xf_cancel");got!=old{t.Fatal("terminal adapter probe mutated pending or poisoned healthy query",probe.name,got,old)}
				var at sql.NullTime;if err:=w.QueryRowContext(p.Ctx,"SELECT pg_stat_get_snapshot_timestamp()").Scan(&at);err!=nil||at.Valid{t.Fatal("terminal adapter probe created cut",probe.name,at,err)}
			}
		}
		if got:=xactFunctionGet(t,p,w,"xf_cancel");got!=old{t.Fatal("cancellation mutated pending",got,old)}
		var at sql.NullTime;if err:=w.QueryRowContext(p.Ctx,"SELECT pg_stat_get_snapshot_timestamp()").Scan(&at);err!=nil||at.Valid{t.Fatal("cancellation created cut",at,err)}
		fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0)
	})
}

func TestReferencePGStatXactUserFunctionsNativeControls(t *testing.T) {
	var emitted []byte
	getters:=[]string{"pg_stat_get_xact_function_calls","pg_stat_get_xact_function_total_time","pg_stat_get_xact_function_self_time"}
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();for _,getter:=range getters{if _,err:=c.ExecContext(ctx,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC");err!=nil{t.Error(err)}}}()
		if b.PG {
			if len(emitted)==0{t.Fatal("missing native dump")};if _,err:=c.ExecContext(p.Ctx,string(emitted));err!=nil{t.Fatal("real oracle dump replay",err)}
			xactFunctionAbsent(t,p,b,c,"xf_control")
			for _,getter:=range getters{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT has_function_privilege('pg_signal_backend',$1,'EXECUTE')",[]string{"has_function_privilege"},[]string{"BOOL"},[][]any{{false}},getter+"(oid)"))}
			return
		}
		xactFunctionCreate(t,p,b,c,"xf_control","RETURN 7;")
		captured,err:=c.QueryContext(p.Ctx,"SELECT * FROM pg_stat_xact_user_functions WHERE FALSE");if err!=nil{t.Fatal(err)};retainedMetadata,err:=captured.ColumnTypes();if err!=nil{t.Fatal(err)};captured.Close();xactFunctionMetadata(t,retainedMetadata,false)
		var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
		for _,getter:=range getters{if strings.Contains(dump.String(),getter){t.Fatal("default authority serialized",getter,dump.String())}}
		for _,getter:=range getters{fixture.StatisticsExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC",0)}
		dump.Reset();if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)};emitted=bytes.Clone(dump.Bytes())
		for _,getter:=range getters{if !strings.Contains(string(emitted),getter){t.Fatal("explicit authority missing",getter,dump.String())}}
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"xf_control",7);old:=xactFunctionGet(t,p,w,"xf_control");fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);retire()
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(emitted));err!=nil{t.Fatal(err)}
		xactFunctionAbsent(t,p,b,c,"xf_control")
		for _,getter:=range getters{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT has_function_privilege('pg_signal_backend',$1,'EXECUTE')",[]string{"has_function_privilege"},[]string{"BOOL"},[][]any{{false}},getter+"(oid)"))}
		if old.Calls!=1||old.Name!="xf_control"{t.Fatal("retained controls row",old)}
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		xactFunctionMetadata(t,retainedMetadata,false)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_stat_xact_user_functions",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM pg_stat_xact_user_functions");if err!=nil{t.Fatal(err)};cols,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,cols,false);rows.Close()
	})
}
