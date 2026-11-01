package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These expectations follow the active-statement snapshot contract: descendant
// recipes run after the barrier, but export the cut selected before its wait.
// Only the descendant, never the named parent, owns the exporting recipe.
func TestReferenceSnapshotSyncDescendantWrites(t *testing.T) {
	for _, route := range []string{"trigger", "check", "generation", "incoming-action", "cte-trigger"} {
		for _, held := range []bool{false, true} {
			name := route
			if held { name += "-held" }
			t.Run(name, func(t *testing.T) { snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, holder, c, observer := snapshotConn(t,p,b), snapshotConn(t,p,b), snapshotConn(t,p,b), snapshotConn(t,p,b)
				syncSeed(t,p,holder)
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_e(id INT,v INT)",0)
				fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_parent(id INT,v INT)",0)
				if route=="generation"{
					fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_descendant_generation(x INT) RETURNS TEXT IMMUTABLE LANGUAGE SQL AS $$SELECT pg_export_snapshot() WHERE x IS NOT NULL$$",0)
					fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_child(token TEXT GENERATED ALWAYS AS (sync_descendant_generation(v)) STORED) INHERITS(sync_parent)",0)
				}else{fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_child() INHERITS(sync_parent)",0)}
				switch route {
				case "trigger", "cte-trigger":
					fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_descendant_export() RETURNS trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM set_config('sync.token',pg_export_snapshot(),false);RETURN NEW;END$$",0)
					fixture.LockExec(t,p.Ctx,holder,"CREATE TRIGGER sync_before BEFORE UPDATE OF v ON sync_child FOR EACH ROW EXECUTE FUNCTION sync_descendant_export()",0)
				case "check":
					fixture.LockExec(t,p.Ctx,holder,"ALTER TABLE sync_child ADD CHECK(set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL)",0)
				case "incoming-action":
					fixture.LockExec(t,p.Ctx,holder,"ALTER TABLE sync_child ADD PRIMARY KEY(id)",0)
					fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_child(id,v) VALUES(3,30)",1)
					fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_descendant_default() RETURNS INT STABLE LANGUAGE SQL AS $$SELECT CASE WHEN set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL THEN 3 ELSE 3 END$$",0)
					fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_fk(fk INT DEFAULT sync_descendant_default() REFERENCES sync_child(id) ON UPDATE SET DEFAULT)",0)
				}
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_child(id,v) VALUES(1,10)",1)
				if route == "incoming-action" { fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_fk(fk) VALUES(1)",1) }
				command := "UPDATE sync_parent SET v=20 WHERE id=1 AND CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN id=1 ELSE id IS NOT NULL END"
				if route == "incoming-action" { command = "UPDATE sync_parent SET id=2 WHERE id=1 AND CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN id=1 ELSE id IS NOT NULL END" }
				if route == "cte-trigger" { command = "WITH changed AS ("+command+" RETURNING id) SELECT id FROM changed" }
				var statement *sql.Stmt
				if held { var err error; statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close() }
				waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
				fixture.LockExec(t,p.Ctx,a,"SET sync.token='pristine'",0)
				fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
				fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(4294967298::bigint)",1)
				join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{
					var result sql.Result;var err error
					if held { result,err=statement.ExecContext(ctx) } else { result,err=a.ExecContext(ctx,command) }
					return fixture.RelationOutcome{Result:result,Err:err}
				})
				advisoryXactWait(t,p,observer,waiterPID,holderPID,advisorySessionKeys[0],false)
				fixture.LockExec(t,p.Ctx,holder,"UPDATE sync_t SET v=20 WHERE id=1",1)
				fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_e VALUES(2,20)",1)
				fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
				out:=join();if out.Err!=nil{t.Fatal(out.Err)};fixture.LockAffected(t,out.Result,1)
				query := "SELECT current_setting('sync.token')"
				if route == "generation" { query = "SELECT token FROM sync_child WHERE id=1" }
				var token string;if err:=a.QueryRowContext(p.Ctx,query).Scan(&token);err!=nil{t.Fatal(err)}
				if token==""||token=="pristine"{t.Fatal("descendant failed to export")}
				fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
				visible:=int64(10);var emptyRows [][]any
				if route=="incoming-action"{visible=20;emptyRows=[][]any{{int64(2),int64(20)}}}
				syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),visible},{int64(3),int64(30)}})
				syncRows(t,p,b,c,"sync_e",emptyRows)
				want:=[][]any{{int64(1),int64(10)}}
				if route=="incoming-action"{want=append(want,[]any{int64(3),int64(30)})}
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT id,v FROM sync_child ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},want))
				if route=="incoming-action"{
					fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT fk FROM sync_fk",[]string{"fk"},[]string{"INT4"},[][]any{{int64(1)}}))
					fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT fk FROM sync_fk",[]string{"fk"},[]string{"INT4"},[][]any{{int64(3)}}))
				}
				fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
				syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),visible},{int64(3),int64(30)}})
				fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0);syncExpired(t,p,c,token)
			}) })
		}
	}
}
