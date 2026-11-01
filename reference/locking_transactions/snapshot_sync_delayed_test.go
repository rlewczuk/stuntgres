package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncDelayedExport(t *testing.T){
	const expression="CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN pg_export_snapshot() ELSE pg_export_snapshot() END"
	for _,route:=range []string{"direct","default","returning","sql-body","dynamic"}{
		for _,held:=range []bool{false,true}{name:=route;if held{name+="-held"};t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,holder,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,holder);fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_e(id INT,v INT)",0)
			waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
			query:="SELECT "+expression
			switch route{
			case "default":
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_delayed_default() RETURNS TEXT STABLE LANGUAGE SQL AS $$SELECT "+expression+"$$",0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_tokens(id INT,token TEXT DEFAULT sync_delayed_default())",0);query="INSERT INTO sync_tokens(id) VALUES(1) RETURNING token"
			case "returning":fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_tokens(id INT)",0);query="INSERT INTO sync_tokens VALUES(1) RETURNING "+expression
			case "sql-body":fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_delayed() RETURNS TEXT BEGIN ATOMIC SELECT "+expression+"; END",0);query="SELECT sync_delayed()"
			case "dynamic":fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_delayed() RETURNS TEXT LANGUAGE plpgsql AS $$DECLARE token TEXT;BEGIN EXECUTE 'SELECT "+expression+"' INTO token;RETURN token;END$$",0);query="SELECT sync_delayed()"
			}
			var statement *sql.Stmt;var err error
			if held{statement,err=a.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()}
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(4294967298::bigint)",1)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{var rows *sql.Rows;var err error;if held{rows,err=statement.QueryContext(ctx)}else{rows,err=a.QueryContext(ctx,query)};return fixture.RelationOutcome{Rows:rows,Err:err}})
			advisoryXactWait(t,p,observer,waiterPID,holderPID,advisorySessionKeys[0],false)
			// The exporter is reached only after this committed publication.
			// Its already-active statement cut must not be recaptured at Eval.
			fixture.LockExec(t,p.Ctx,holder,"UPDATE sync_t SET v=20 WHERE id=1",1);fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_e VALUES(2,20)",1)
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
			out:=join();if out.Err!=nil{t.Fatal(out.Err)};defer out.Rows.Close()
			if !out.Rows.Next(){t.Fatal("missing export",out.Rows.Err())};var token string;if err:=out.Rows.Scan(&token);err!=nil{t.Fatal(err)};if out.Rows.Next()||out.Rows.Err()!=nil{t.Fatal("extra export row",out.Rows.Err())};out.Rows.Close()
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}});syncRows(t,p,b,c,"sync_e",nil)
		})})}
	}
}
