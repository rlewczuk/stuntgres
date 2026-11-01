package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncAlterExpressions(t *testing.T) {
	for _,route:=range []string{"check","validate","default","generation","using","index-key","index-predicate","inherited","domain-default","domain-using","preflight-no-rewrite"}{
		for _,held:=range []bool{false,true}{name:=route;if held{name+="-held"};t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			var notices []fixture.Diagnostic
			_,a,_:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
			defer func(){_,_=a.ExecContext(p.Ctx,"ROLLBACK")}()
			holder,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,holder)
			fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_alter_unread(id INT,v INT)",0)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_alter_unread VALUES(1,10)",1)
			fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_alter_empty(id INT,v INT)",0)
			definition:="CREATE TABLE sync_alter(id INT,v INT)"
			if route=="generation"{definition="CREATE TABLE sync_alter(id INT,v INT,token TEXT GENERATED ALWAYS AS(v::text) STORED)"}
			fixture.LockExec(t,p.Ctx,holder,definition,0)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_alter(id,v) VALUES(1,10)",1)
			const noticeBody="DECLARE token TEXT;BEGIN PERFORM pg_advisory_xact_lock(4294967298::bigint);token:=pg_export_snapshot();RAISE NOTICE 'snapshot:%',token;"
			callback:="sync_alter_token()"
			fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_token() RETURNS TEXT STABLE LANGUAGE plpgsql AS $$"+noticeBody+"RETURN token;END$$",0)
			const token="sync_alter_token()"
			command:="ALTER TABLE sync_alter ADD CONSTRAINT sync_check CHECK (("+token+") IS NOT NULL)"
			switch route {
			case "validate":
				fixture.LockExec(t,p.Ctx,holder,command+" NOT VALID",0)
				command="ALTER TABLE sync_alter VALIDATE CONSTRAINT sync_check"
			case "default":
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_default() RETURNS TEXT VOLATILE LANGUAGE plpgsql AS $$"+noticeBody+"RETURN token;END$$",0)
				command="ALTER TABLE sync_alter ADD COLUMN token TEXT DEFAULT sync_alter_default()"
				callback="sync_alter_default()"
			case "generation":
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_generation(x INT) RETURNS TEXT IMMUTABLE LANGUAGE plpgsql AS $$"+noticeBody+"RETURN token;END$$",0)
				callback="sync_alter_generation(integer)"
				command="ALTER TABLE sync_alter ALTER COLUMN token SET EXPRESSION AS (sync_alter_generation(v))"
			case "using": command="ALTER TABLE sync_alter ALTER COLUMN v TYPE TEXT USING (CASE WHEN ("+token+") IS NOT NULL THEN v::text ELSE v::text END)"
			case "index-key","index-predicate":
				result,ordinary,returned:="INT","x","x"
				if route=="index-predicate"{result,ordinary,returned="BOOL","x>0","TRUE"}
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_index(x BIGINT) RETURNS "+result+" IMMUTABLE LANGUAGE SQL AS $$SELECT ("+ordinary+")::"+result+"$$",0)
				indexSQL:="CREATE INDEX sync_alter_index ON sync_alter(sync_alter_index(v))"
				if route=="index-predicate"{indexSQL="CREATE INDEX sync_alter_index ON sync_alter(v) WHERE sync_alter_index(v)"}
				fixture.LockExec(t,p.Ctx,holder,indexSQL,0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE OR REPLACE FUNCTION sync_alter_index(x BIGINT) RETURNS "+result+" IMMUTABLE LANGUAGE plpgsql AS $$"+noticeBody+"RETURN "+returned+";END$$",0)
				callback="public.sync_alter_index(bigint)"
				command="ALTER TABLE sync_alter ALTER COLUMN v TYPE BIGINT"
			case "inherited":
				// Only the child executes the exporter. Parent DEFAULT preflight
				// fixes the pair before the later descendant physical phase.
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_alter_child() INHERITS(sync_alter)",0)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_alter_child VALUES(2,20),(3,30)",2)
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_child_check(x BIGINT) RETURNS BOOL STABLE LANGUAGE SQL AS $$SELECT true$$",0)
				fixture.LockExec(t,p.Ctx,holder,"ALTER TABLE ONLY sync_alter_child ADD CHECK (sync_alter_child_check(v))",0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE OR REPLACE FUNCTION sync_alter_child_check(x BIGINT) RETURNS BOOL STABLE LANGUAGE plpgsql AS $$"+noticeBody+"RETURN true;END$$",0)
				callback="sync_alter_child_check(bigint)"
				command="ALTER TABLE sync_alter ADD COLUMN fill INT DEFAULT 7, ALTER COLUMN v TYPE BIGINT"
			case "domain-default","domain-using":
				fixture.LockExec(t,p.Ctx,holder,"CREATE DOMAIN sync_alter_domain AS TEXT CHECK (("+token+") IS NOT NULL)",0)
				command="ALTER TABLE sync_alter ADD COLUMN token sync_alter_domain DEFAULT 'filled'"
				if route=="domain-using"{command="ALTER TABLE sync_alter ALTER COLUMN v TYPE sync_alter_domain USING v::text"}
			case "preflight-no-rewrite":
				// A nonvolatile domain conversion is evaluated during DEFAULT
				// preflight and becomes a captured missing value. An unrelated
				// expression index must not be rebuilt after that conversion.
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_preflight() RETURNS TEXT IMMUTABLE LANGUAGE plpgsql AS $$"+noticeBody+"RETURN token;END$$",0)
				callback="sync_alter_preflight()"
				fixture.LockExec(t,p.Ctx,holder,"CREATE DOMAIN sync_alter_domain AS TEXT CHECK (sync_alter_preflight() IS NOT NULL)",0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_alter_untouched(x INT) RETURNS INT IMMUTABLE LANGUAGE SQL AS $$SELECT x$$",0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE INDEX sync_alter_untouched ON sync_alter(sync_alter_untouched(id))",0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE OR REPLACE FUNCTION sync_alter_untouched(x INT) RETURNS INT IMMUTABLE LANGUAGE SQL AS $$SELECT 1/(x-x)$$",0)
				command="ALTER TABLE sync_alter ADD COLUMN token TEXT DEFAULT (('filled'::sync_alter_domain)::TEXT)"
			}
			var statement *sql.Stmt
			if held{var err error;statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close()}
			waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_t VALUES(9,90)",1)
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(4294967298::bigint)",1)
			completed:=make(chan fixture.RelationOutcome,1)
			join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{var result sql.Result;var err error;if held{result,err=statement.ExecContext(ctx)}else{result,err=a.ExecContext(ctx,command)};out:=fixture.RelationOutcome{Result:result,Err:err};completed<-out;return out})
			syncAdvisoryWait(t,p,observer,waiterPID,holderPID,advisorySessionKeys[0],completed)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_t VALUES(2,20)",1)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_alter_unread VALUES(2,20)",1)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_alter_empty VALUES(3,30)",1)
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
			out:=join();if out.Err!=nil{t.Fatal(out.Err)};fixture.LockAffected(t,out.Result,0)
			wantRows:=[][]any{{int64(1),int64(10)},{int64(3),int64(30)}}
			wantUnread:=[][]any{{int64(1),int64(10)}}
			var wantEmpty [][]any
			if route=="default"{
				// The VOLATILE PL assignment acquires its own RC snapshot
				// after the preceding advisory-lock statement has returned.
				wantRows=[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}}
				wantUnread=[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}
				wantEmpty=[][]any{{int64(3),int64(30)}}
			}
			var exported string
			var tokens []string
			if len(notices)==0{t.Fatal("ALTER expression did not publish a snapshot notice")}
			for _,notice:=range notices{
				if !strings.HasPrefix(notice.Message,"snapshot:"){t.Fatalf("unexpected ALTER notice %#v",notice)}
				exported=strings.TrimPrefix(notice.Message,"snapshot:")
				want:=fixture.ControlsReferenceDiagnostic("00000",notice.Message)
				want.Severity,want.SeverityUnlocalized="NOTICE","NOTICE"
				want.Context="PL/pgSQL function "+callback+" line 1 at RAISE"
				if notice!=want||exported==""{t.Fatalf("snapshot notice got %#v want %#v",notice,want)}
				for _,previous:=range tokens{if previous==exported{t.Fatal("callback reused export identifier")}}
				tokens=append(tokens,exported)
				fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,exported)
				syncRows(t,p,b,c,"sync_t",wantRows)
				syncRows(t,p,b,c,"sync_alter_unread",wantUnread);syncRows(t,p,b,c,"sync_alter_empty",wantEmpty)
				fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
			}
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,exported)
			syncRows(t,p,b,c,"sync_t",wantRows)
			syncRows(t,p,b,c,"sync_alter_unread",wantUnread)
			syncRows(t,p,b,c,"sync_alter_empty",wantEmpty)
			if route=="inherited"{
				fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT id,v,fill FROM sync_alter ORDER BY id",[]string{"id","v","fill"},[]string{"INT4","INT8","INT4"},[][]any{{int64(1),int64(10),int64(7)},{int64(2),int64(20),int64(7)},{int64(3),int64(30),int64(7)}}))
			}
			if route=="domain-default"||route=="preflight-no-rewrite"{
				fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT token::text AS token FROM sync_alter",[]string{"token"},[]string{"TEXT"},[][]any{{"filled"}}))
			}
			fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
			syncRows(t,p,b,c,"sync_t",wantRows)
			fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
			for _,token:=range tokens{syncExpired(t,p,c,token)}
		})})}
	}
}
