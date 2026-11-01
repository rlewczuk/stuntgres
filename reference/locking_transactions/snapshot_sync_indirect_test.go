package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncIndirectCalls(t *testing.T){
	for _,tc:=range []struct{name,setup,query string}{
		{"default","CREATE TABLE sync_tokens(id INT,token TEXT DEFAULT pg_export_snapshot())","INSERT INTO sync_tokens(id) VALUES(1) RETURNING token"},
		{"returning","CREATE TABLE sync_tokens(id INT)","INSERT INTO sync_tokens VALUES(1) RETURNING pg_export_snapshot()"},
		{"update-returning","CREATE TABLE sync_tokens(id INT);INSERT INTO sync_tokens VALUES(1)","UPDATE sync_tokens SET id=2 RETURNING pg_export_snapshot()"},
		{"delete-returning","CREATE TABLE sync_tokens(id INT);INSERT INTO sync_tokens VALUES(1)","DELETE FROM sync_tokens RETURNING pg_export_snapshot()"},
		{"insert-select","CREATE TABLE sync_tokens(token TEXT)","INSERT INTO sync_tokens SELECT pg_export_snapshot() RETURNING token"},
		{"cte","CREATE TABLE sync_tokens(token TEXT)","WITH inserted AS(INSERT INTO sync_tokens VALUES(pg_export_snapshot()) RETURNING token) SELECT token FROM inserted"},
		{"view","CREATE VIEW sync_view AS SELECT pg_export_snapshot() AS token","SELECT token FROM sync_view"},
		{"sql-standard","CREATE FUNCTION sync_export() RETURNS TEXT BEGIN ATOMIC SELECT pg_export_snapshot(); END","SELECT sync_export()"},
		{"sql-stable","CREATE FUNCTION sync_export() RETURNS TEXT LANGUAGE SQL STABLE AS $$SELECT pg_export_snapshot()$$","SELECT sync_export()"},
		{"sql-volatile","CREATE FUNCTION sync_export() RETURNS TEXT LANGUAGE SQL VOLATILE AS $$SELECT pg_export_snapshot()$$","SELECT sync_export()"},
		{"pl-dynamic","CREATE FUNCTION sync_export() RETURNS TEXT LANGUAGE plpgsql AS $$DECLARE token TEXT; BEGIN EXECUTE 'SELECT pg_export_snapshot()' INTO token; RETURN token; END$$","SELECT sync_export()"},
		{"trigger","CREATE TABLE sync_tokens(id INT,token TEXT);CREATE FUNCTION sync_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.token:=pg_export_snapshot();RETURN NEW;END$$;CREATE TRIGGER sync_before BEFORE INSERT ON sync_tokens FOR EACH ROW EXECUTE FUNCTION sync_trigger()","INSERT INTO sync_tokens(id) VALUES(1) RETURNING token"},
	}{
		for _,held:=range []bool{false,true}{name:=tc.name;if held{name+="-held"};t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b);syncSeed(t,p,w)
			// Split only these fixture-owned top-level statements, never function
			// bodies: fixture accepts complete multi-command setup in one call.
			rows,err:=w.QueryContext(p.Ctx,tc.setup);if err!=nil{t.Fatal(err)};for{for rows.Next(){};if !rows.NextResultSet(){break}};if err:=rows.Close();err!=nil{t.Fatal(err)}
			var statement *sql.Stmt
			if held{statement,err=a.PrepareContext(p.Ctx,tc.query);if err!=nil{t.Fatal(err)};defer statement.Close()}
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			var token string
			if held{err=statement.QueryRowContext(p.Ctx).Scan(&token)}else{err=a.QueryRowContext(p.Ctx,tc.query).Scan(&token)}
			if err!=nil||token==""{t.Fatalf("reached indirect export %q: %v",token,err)}
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		})})}
	}
	t.Run("copy-default",func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		db:=p.Native;if b.PG{db=p.CopyPostgres}
		a,err:=db.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer a.Close()
		defer func(){_,_=a.ExecContext(p.Ctx,"ROLLBACK")}()
		w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b);syncSeed(t,p,w)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_tokens(id INT,token TEXT DEFAULT pg_export_snapshot())",0);fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		result,err:=fixture.CopyFrom(p.Ctx,a,b.PG,"COPY sync_tokens(id) FROM STDIN",strings.NewReader("1\n"));if err!=nil||result.Count!=1||result.CommandTag!="COPY 1"{t.Fatal(result,err)}
		var token string;if err:=a.QueryRowContext(p.Ctx,"SELECT token FROM sync_tokens").Scan(&token);err!=nil{t.Fatal(err)}
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1);fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
	})})
}
