package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

const pgTriggerLiveQuery = "SELECT tgname,tgtype,tgenabled,tgnargs,encode(tgargs,'hex') AS args,tgattr::text AS attrs,tgqual IS NOT NULL AS has_qual FROM pg_trigger WHERE tgrelid=$1::regclass ORDER BY tgname"
var pgTriggerLiveColumns = []string{"tgname","tgtype","tgenabled","tgnargs","args","attrs","has_qual"}
var pgTriggerLiveTypes = []string{"NAME","INT2","CHAR","INT2","TEXT","TEXT","BOOL"}

func pgTriggerLive(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,table string,rows [][]any) {
	t.Helper()
	s:=fixture.Select(pgTriggerLiveQuery,pgTriggerLiveColumns,pgTriggerLiveTypes,rows,table)
	fixture.PgDescriptionExecutorRows(t,p,b,e,s)
	fixture.PgDescriptionExecutorRows(t,p,b,e,fixture.Prepared(s))
}

func pgTriggerSmokeSetup(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	pgTriggerExec(t,p,b,b.DB,"CREATE TABLE pgt_smoke(id integer,v integer)")
	pgTriggerExec(t,p,b,b.DB,"CREATE FUNCTION pgt_smoke_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v*2; RETURN NEW; END$$")
	pgTriggerLive(t,p,b,b.DB,"pgt_smoke",[][]any{})
	pgTriggerExec(t,p,b,b.DB,"CREATE TRIGGER pgt_smoke_trigger BEFORE INSERT ON pgt_smoke FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION pgt_smoke_fn('x','','é')")
}

func pgTriggerSmokeRow(name string) [][]any { return [][]any{{name,int64(7),"O",int64(3),"780000c3a900","",true}} }

func TestReferencePGTriggerBehaviorLifecycle(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			pgTriggerSmokeSetup(t,p,b)
			pgTriggerLive(t,p,b,b.DB,"pgt_smoke",pgTriggerSmokeRow("pgt_smoke_trigger"))
			fixture.PgDescriptionRows(t,p,b,fixture.Select("INSERT INTO pgt_smoke VALUES(1,5) RETURNING v",[]string{"v"},[]string{"INT4"},[][]any{{int64(10)}}))
			pgTriggerExec(t,p,b,b.DB,"CREATE TABLE pgt_log(v integer)")
			pgTriggerExec(t,p,b,b.DB,"CREATE FUNCTION pgt_statement_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO pgt_log VALUES(1); RETURN NULL; END$$")
			pgTriggerExec(t,p,b,b.DB,"CREATE TRIGGER pgt_statement AFTER UPDATE ON pgt_smoke FOR EACH STATEMENT EXECUTE FUNCTION pgt_statement_fn()")
			pgTriggerExec(t,p,b,b.DB,"UPDATE pgt_smoke SET v=v WHERE FALSE")
			pgTriggerBoth(t,p,b,fixture.Select("SELECT count(*) AS n FROM pgt_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}}))
			pgTriggerBoth(t,p,b,fixture.Select("SELECT tgtype FROM pg_trigger WHERE tgrelid='pgt_smoke'::regclass AND tgname='pgt_statement'",[]string{"tgtype"},[]string{"INT2"},[][]any{{int64(16)}}))
			pgTriggerExec(t,p,b,b.DB,"CREATE TABLE pgt_target(a integer,b integer,c integer)")
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO pgt_target VALUES(1,2,3)",1))
			pgTriggerExec(t,p,b,b.DB,"CREATE TRIGGER pgt_columns AFTER UPDATE OF c,a ON pgt_target FOR EACH ROW EXECUTE FUNCTION pgt_statement_fn()")
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("UPDATE pgt_target SET b=b",1))
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("UPDATE pgt_target SET a=a",1))
			pgTriggerLive(t,p,b,b.DB,"pgt_target",[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false}})
			pgTriggerBoth(t,p,b,fixture.Select("SELECT count(*) AS n FROM pgt_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}}))
			c,peer:=fixture.LockConnections(t,p.Ctx,b.DB)
			stmt,err:=c.PrepareContext(p.Ctx,pgTriggerLiveQuery)
			if err!=nil { t.Fatal(err) }; defer stmt.Close()
			held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:pgTriggerLiveQuery}
			pgTriggerExec(t,p,b,c,"BEGIN"); defer c.ExecContext(p.Ctx,"ROLLBACK")
			pgTriggerExec(t,p,b,c,"DROP TRIGGER pgt_columns ON pgt_target")
			pgTriggerLive(t,p,b,c,"pgt_target",[][]any{})
			pgTriggerLive(t,p,b,peer,"pgt_target",[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false}})
			pgTriggerExec(t,p,b,c,"ROLLBACK")
			fixture.PgDescriptionExecutorRows(t,p,b,held,fixture.Select(pgTriggerLiveQuery,pgTriggerLiveColumns,pgTriggerLiveTypes,[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false}},"pgt_target"))
			pgTriggerExec(t,p,b,c,"BEGIN")
			pgTriggerExec(t,p,b,c,"CREATE TRIGGER pgt_committed AFTER DELETE ON pgt_target FOR EACH STATEMENT EXECUTE FUNCTION pgt_statement_fn()")
			pgTriggerLive(t,p,b,c,"pgt_target",[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false},{"pgt_committed",int64(8),"O",int64(0),"","",false}})
			pgTriggerLive(t,p,b,peer,"pgt_target",[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false}})
			pgTriggerExec(t,p,b,c,"ROLLBACK")
			pgTriggerLive(t,p,b,c,"pgt_target",[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false}})
			pgTriggerExec(t,p,b,c,"BEGIN")
			pgTriggerExec(t,p,b,c,"CREATE TRIGGER pgt_committed AFTER DELETE ON pgt_target FOR EACH STATEMENT EXECUTE FUNCTION pgt_statement_fn()")
			pgTriggerExec(t,p,b,c,"COMMIT")
			pgTriggerLive(t,p,b,peer,"pgt_target",[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false},{"pgt_committed",int64(8),"O",int64(0),"","",false}})
			fixture.PgDescriptionExecutorRows(t,p,b,held,fixture.Select(pgTriggerLiveQuery,pgTriggerLiveColumns,pgTriggerLiveTypes,[][]any{{"pgt_columns",int64(17),"O",int64(0),"","3 1",false},{"pgt_committed",int64(8),"O",int64(0),"","",false}},"pgt_target"))
		})
	}
}
