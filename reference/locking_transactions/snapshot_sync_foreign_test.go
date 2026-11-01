package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncForeignActions(t *testing.T) {
	for _,route:=range []string{"set-default","not-valid","not-enforced","child-trigger","transitive","modifying-cte","default-replaced","routine-replaced","trigger-replaced","cycle"}{
		for _, held := range []bool{false,true} {
		name:=route;if held{name+="-held"}
		t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,holder,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,holder);fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_e(id INT,v INT)",0)
			if route=="cycle" {
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_parent(id TEXT PRIMARY KEY,parent TEXT REFERENCES sync_parent(id) ON UPDATE CASCADE)",0)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_parent VALUES('old','old'),('fallback',NULL)",2)
			} else {
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_parent(id TEXT PRIMARY KEY)",0)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_parent VALUES('old'),('fallback')",2)
			}
			const exportBody="SELECT CASE WHEN pg_advisory_xact_lock(1,2) IS NULL THEN CASE WHEN set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL THEN 'fallback'::text ELSE 'fallback'::text END ELSE CASE WHEN set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL THEN 'fallback'::text ELSE 'fallback'::text END END"
			fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_export_default() RETURNS TEXT STABLE LANGUAGE SQL AS $$"+exportBody+"$$",0)
			defaultSQL:="sync_export_default()"
			exportDefault := defaultSQL
			if route=="default-replaced" { defaultSQL="'fallback'" }
			if route=="routine-replaced" {
				fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_default_value() RETURNS TEXT STABLE BEGIN ATOMIC SELECT 'fallback'::text; END",0)
				defaultSQL="sync_default_value()"
			}
			barrier := " WHERE id='old' AND CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN id='old' ELSE id IS NOT NULL END"
			command := "DELETE FROM sync_parent" + barrier
			if route=="cycle" { command = "UPDATE sync_parent SET id='new'" + barrier }
			if route=="transitive"{
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_child(id TEXT PRIMARY KEY REFERENCES sync_parent(id) ON UPDATE CASCADE)",0)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_child VALUES('old'),('fallback')",2)
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_grandchild(id INT PRIMARY KEY,fk TEXT DEFAULT ("+defaultSQL+") REFERENCES sync_child(id) ON UPDATE SET DEFAULT)",0)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_grandchild(id,fk) VALUES(1,'old')",1)
				command = "UPDATE sync_parent SET id='new'" + barrier
			}else{
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_child(id INT PRIMARY KEY,fk TEXT DEFAULT ("+defaultSQL+"),marker TEXT)",0)
				action,suffix:="SET DEFAULT",""
				if route=="not-valid"{suffix=" NOT VALID"}
				if route=="not-enforced"{suffix=" NOT ENFORCED"}
				if route=="child-trigger" || route=="trigger-replaced" {
					action="SET NULL"
					fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_child_export() RETURNS trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM pg_advisory_xact_lock(1,2);NEW.marker:=pg_export_snapshot();PERFORM set_config('sync.token',NEW.marker,false);RETURN NEW;END$$",0)
					if route=="trigger-replaced" {
						fixture.LockExec(t,p.Ctx,holder,"CREATE OR REPLACE FUNCTION sync_child_export() RETURNS trigger LANGUAGE plpgsql STABLE AS $$BEGIN RETURN NEW;END$$",0)
					}
					fixture.LockExec(t,p.Ctx,holder,"CREATE TRIGGER sync_child_before BEFORE UPDATE ON sync_child FOR EACH ROW EXECUTE FUNCTION sync_child_export()",0)
				}
				actions := " ON DELETE "+action
				if route=="cycle" { actions += " ON UPDATE SET DEFAULT" }
				fixture.LockExec(t,p.Ctx,holder,"ALTER TABLE sync_child ADD CONSTRAINT sync_fk FOREIGN KEY(fk) REFERENCES sync_parent(id)"+actions+suffix,0)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_child(id,fk) VALUES(1,'old')",1)
			}
			if route=="set-default"{command+=" RETURNING id,set_config('sync.outer',pg_export_snapshot(),false)"}else if route=="modifying-cte"{command+=" RETURNING id"}
			if route == "modifying-cte" {
				command = "WITH deleted AS (" + command + ") SELECT id FROM deleted"
			}
			var statement *sql.Stmt
			if held { var err error;statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close() }
			// A held parent command must consume the current child descriptor
			// and selected routine closure, not a stale transitive export bit.
			switch route {
			case "default-replaced":
				fixture.LockExec(t,p.Ctx,holder,"ALTER TABLE sync_child ALTER COLUMN fk SET DEFAULT ("+exportDefault+")",0)
			case "routine-replaced":
				fixture.LockExec(t,p.Ctx,holder,"CREATE OR REPLACE FUNCTION sync_default_value() RETURNS TEXT STABLE BEGIN ATOMIC "+exportBody+"; END",0)
			case "trigger-replaced":
				fixture.LockExec(t,p.Ctx,holder,"CREATE OR REPLACE FUNCTION sync_child_export() RETURNS trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM pg_advisory_xact_lock(1,2);NEW.marker:=pg_export_snapshot();PERFORM set_config('sync.token',NEW.marker,false);RETURN NEW;END$$",0)
			}
			waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
			fixture.LockExec(t,p.Ctx,a,"SET sync.token='pristine'",0)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			fixture.LockExec(t,p.Ctx,a,"DELETE FROM sync_parent WHERE id='missing'",0)
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT current_setting('sync.token') AS token",[]string{"token"},[]string{"TEXT"},[][]any{{"pristine"}}))
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(4294967298::bigint)",1)
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(1,2)",1)
			completed:=make(chan fixture.RelationOutcome,1)
			join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{
				var result sql.Result;var err error
				if held { result,err=statement.ExecContext(ctx) } else { result,err=a.ExecContext(ctx,command) }
				out:=fixture.RelationOutcome{Result:result,Err:err};completed<-out;return out
			})
			syncAdvisoryWait(t,p,observer,waiterPID,holderPID,advisorySessionKeys[0],completed)
			fixture.LockExec(t,p.Ctx,holder,"UPDATE sync_t SET v=20 WHERE id=1",1);fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_e VALUES(2,20)",1)
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
			if route!="not-enforced"{
				syncAdvisoryWait(t,p,observer,waiterPID,holderPID,advisorySessionKeys[1],completed)
				fixture.LockExec(t,p.Ctx,holder,"UPDATE sync_t SET v=30 WHERE id=1",1)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_e VALUES(3,30)",1)
			}
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(1,2) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
			out:=join();if out.Err!=nil{t.Fatal(out.Err)};fixture.LockAffected(t,out.Result,1)
			if route=="set-default"{
				var outer string;if err:=a.QueryRowContext(p.Ctx,"SELECT current_setting('sync.outer')").Scan(&outer);err!=nil||outer==""{t.Fatal(outer,err)}
				fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,outer)
				syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}});syncRows(t,p,b,c,"sync_e",nil)
				fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
			}
			var token string;if err:=a.QueryRowContext(p.Ctx,"SELECT current_setting('sync.token')").Scan(&token);err!=nil{t.Fatal(err)}
			if route=="not-enforced"{
				if token!="pristine"{t.Fatal("unenforced action executed exporter")}
			}else{
				if token==""||token=="pristine"{t.Fatal("action failed to publish real token")}
				fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
				syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(20)},{int64(3),int64(30)}});syncRows(t,p,b,c,"sync_e",[][]any{{int64(2),int64(20)}})
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT id FROM sync_parent ORDER BY id",[]string{"id"},[]string{"TEXT"},[][]any{{"fallback"},{"old"}}))
				if route == "transitive" {
					fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT fk FROM sync_grandchild",[]string{"fk"},[]string{"TEXT"},[][]any{{"old"}}))
					fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT fk FROM sync_grandchild",[]string{"fk"},[]string{"TEXT"},[][]any{{"fallback"}}))
				} else {
					fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT fk,marker FROM sync_child",[]string{"fk","marker"},[]string{"TEXT","TEXT"},[][]any{{"old",nil}}))
					var want any = "fallback"
					var marker any
					if route == "child-trigger" || route == "trigger-replaced" { want, marker = nil, token }
					fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT fk,marker FROM sync_child",[]string{"fk","marker"},[]string{"TEXT","TEXT"},[][]any{{want,marker}}))
				}
				fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
			}
			if route == "not-enforced" {
				fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0)
			} else {
				fixture.PreparedReferenceCleanup(t,b,"sync_fk_reset")
				syncError(t,p,a,"PREPARE TRANSACTION 'sync_fk_reset'","0A000","cannot PREPARE a transaction that has exported snapshots")
				fixture.PreparedReferenceAbsent(t,p,holder,"sync_fk_reset")
				syncExpired(t,p,c,token)
			}
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT id FROM sync_parent ORDER BY id",[]string{"id"},[]string{"TEXT"},[][]any{{"fallback"},{"old"}}))
		})})
		}
	}
}
