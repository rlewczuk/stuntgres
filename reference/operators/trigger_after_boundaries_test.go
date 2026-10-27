package reference_test

import (
	"database/sql"
	"testing"
	"unicode/utf8"
	"strings"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerAfterTruncate(t *testing.T) {
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_a(id integer GENERATED ALWAYS AS IDENTITY)",
			"CREATE TABLE aft_b(id integer)",
			"CREATE TABLE aft_trlog(name text,op text,level text,when_text text,a bigint,b bigint)",
			"CREATE FUNCTION aft_trunc() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_trlog VALUES(TG_TABLE_NAME,TG_OP,TG_LEVEL,TG_WHEN,(SELECT count(*) FROM aft_a),(SELECT count(*) FROM aft_b)); IF TG_TABLE_NAME='aft_b' THEN INSERT INTO aft_a DEFAULT VALUES; END IF; RETURN NULL; END$$",
			"CREATE FUNCTION aft_wrong_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
			"CREATE TRIGGER trunc AFTER TRUNCATE ON aft_a EXECUTE FUNCTION aft_trunc()",
			"CREATE TRIGGER trunc AFTER TRUNCATE ON aft_b FOR EACH STATEMENT EXECUTE FUNCTION aft_trunc()",
			"CREATE TRIGGER del AFTER DELETE ON aft_a EXECUTE FUNCTION aft_wrong_delete()")
		run:=func(s fixture.Step){ fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("TRUNCATE aft_a",0))
		run(fixture.Exec("INSERT INTO aft_a DEFAULT VALUES",1))
		run(fixture.Exec("INSERT INTO aft_b VALUES(1)",1))
		run(fixture.Exec("TRUNCATE aft_trlog",0))
		run(fixture.Exec("TRUNCATE aft_a,aft_b RESTART IDENTITY",0))
		s:=fixture.Select("SELECT name,op,level,when_text,a,b FROM aft_trlog",[]string{"name","op","level","when_text","a","b"},[]string{"TEXT","TEXT","TEXT","TEXT","INT8","INT8"},[][]any{{"aft_a","TRUNCATE","STATEMENT","AFTER",int64(0),int64(0)},{"aft_b","TRUNCATE","STATEMENT","AFTER",int64(0),int64(0)}}); s.Bag=true; run(s)
		run(fixture.Select("SELECT id FROM aft_a",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION aft_trfail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
			"CREATE TRIGGER z AFTER TRUNCATE ON aft_b EXECUTE FUNCTION aft_trfail()")
		run(fixture.Error("TRUNCATE aft_a,aft_b RESTART IDENTITY","22012"))
		run(fixture.Select("SELECT id FROM aft_a",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
		run(fixture.Exec("INSERT INTO aft_a DEFAULT VALUES",1))
		run(fixture.Select("SELECT id FROM aft_a ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		run(fixture.Select("SELECT count(*) AS n FROM aft_trlog",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}}))
	})
}
func TestReferenceTriggerAfterRollback(t *testing.T) {
	for _,op:=range []string{"INSERT","UPDATE","DELETE","TRUNCATE"} {
		t.Run(op,func(t *testing.T){
			afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool){
				afterSetup(t,p,b,c)
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO aft(id,v) VALUES(1,10),(2,20)",2))
				userOperatorExec(t,p,b,c,
					"CREATE FUNCTION aft_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
					"CREATE TRIGGER a AFTER "+op+" ON aft EXECUTE FUNCTION aft_statement()",
					"CREATE TRIGGER z AFTER "+op+" ON aft EXECUTE FUNCTION aft_fail()")
				run:=func(s fixture.Step){ fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				q:="INSERT INTO aft(id,v) VALUES(3,30)"
				switch op {case "UPDATE":q="UPDATE aft SET v=99";case "DELETE":q="DELETE FROM aft";case "TRUNCATE":q="TRUNCATE aft"}
				d:=fixture.ControlsReferenceDiagnostic("22012","division by zero"); d.Context="SQL statement \"SELECT 1/0\"\nPL/pgSQL function aft_fail() line 1 at PERFORM"
				fail:=fixture.ErrorDiagnostic(q,d)
				if prepared && op!="TRUNCATE" {fail=fixture.Prepared(fail)}
				run(fail)
				if op!="TRUNCATE" {
					queryFail:=fixture.ErrorDiagnostic(q+" RETURNING id,v",d);queryFail.SelectRows=true
					if prepared {queryFail=fixture.Prepared(queryFail)}
					run(queryFail)
				}
				check:=func(){
					run(triggerRows("SELECT id,v FROM aft ORDER BY id",[]any{int64(1),int64(10)},[]any{int64(2),int64(20)}))
					run(fixture.Select("SELECT count(*) AS n FROM aft_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
				}
				check()
				run(fixture.Exec("BEGIN",0));run(fixture.Exec("SAVEPOINT s",0));run(fail)
				run(fixture.ErrorDiagnostic("SELECT 1",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
				run(fixture.Exec("ROLLBACK TO SAVEPOINT s",0));check();run(fixture.Exec("COMMIT",0))
				userOperatorExec(t,p,b,c,"DROP TRIGGER z ON aft")
				n:=int64(2);if op=="INSERT" {n=1};if op=="TRUNCATE" {n=0}
				run(fixture.Exec(q,n))
			})
		})
	}
}
func TestReferenceTriggerAfterDiagnostics(t *testing.T) {
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		afterSetup(t,p,b,c)
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.ErrorDiagnostic("CREATE TRIGGER bad AFTER TRUNCATE ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()",fixture.ControlsReferenceDiagnostic("0A000","TRUNCATE FOR EACH ROW triggers are not supported")))
		for _,ref:=range []string{"NEW.v","OLD.v","NEW IS NULL"} {
			q:="/* é */ CREATE TRIGGER bad AFTER UPDATE ON aft WHEN("+ref+">0) EXECUTE FUNCTION aft_row()"
			if ref=="NEW IS NULL" {q="/* é */ CREATE TRIGGER bad AFTER UPDATE ON aft WHEN(NEW IS NULL) EXECUTE FUNCTION aft_row()"}
			d:=fixture.ControlsReferenceDiagnostic("42P17","statement trigger's WHEN condition cannot reference column values")
			position:=strings.Index(q,"NEW");if position<0 {position=strings.Index(q,"OLD")};d.Position=fixture.Position(utf8.RuneCountInString(q[:position])+1)
			run(fixture.ErrorDiagnostic(q,d))
		}
		for _,level:=range []string{"ROW","STATEMENT"} {
			userOperatorExec(t,p,b,c,"CREATE OR REPLACE FUNCTION aft_missing() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN END$$","CREATE OR REPLACE TRIGGER missing AFTER INSERT ON aft FOR EACH "+level+" EXECUTE FUNCTION aft_missing()")
			d:=fixture.ControlsReferenceDiagnostic("2F005","control reached end of trigger procedure without RETURN");d.Context="PL/pgSQL function aft_missing()"
			run(fixture.ErrorDiagnostic("INSERT INTO aft(id,v) VALUES(1,10)",d))
		}
		userOperatorExec(t,p,b,c,"DROP TRIGGER missing ON aft","CREATE FUNCTION aft_badrow() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN ROW(1); END$$","CREATE TRIGGER bad AFTER INSERT ON aft FOR EACH ROW EXECUTE FUNCTION aft_badrow()")
		d:=fixture.ControlsReferenceDiagnostic("42804","returned row structure does not match the structure of the triggering table");d.Detail="Number of returned columns (1) does not match expected column count (4).";d.Context="PL/pgSQL function aft_badrow() during function exit"
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO aft(id,v) VALUES(1,10)",d)))
		run(fixture.Select("SELECT count(*) AS n FROM aft",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
	})
}
func TestReferenceTriggerAfterQueueOwnership(t *testing.T) {
	afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool){
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_x(id integer)","CREATE TABLE aft_y(id integer)","CREATE TABLE aft_events(name text,level text,id integer,n bigint)",
			"CREATE FUNCTION aft_cte() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_events VALUES(TG_TABLE_NAME,TG_LEVEL,NEW.id,(SELECT count(*) FROM aft_x)+(SELECT count(*) FROM aft_y)); RETURN NULL; END$$",
			"CREATE TRIGGER r AFTER INSERT ON aft_x FOR EACH ROW EXECUTE FUNCTION aft_cte()","CREATE TRIGGER s AFTER INSERT ON aft_x EXECUTE FUNCTION aft_cte()",
			"CREATE TRIGGER r AFTER INSERT ON aft_y FOR EACH ROW EXECUTE FUNCTION aft_cte()","CREATE TRIGGER s AFTER INSERT ON aft_y EXECUTE FUNCTION aft_cte()")
		run:=func(s fixture.Step){afterStep(t,p,b,c,prepared,s)}
		run(fixture.Select("WITH x AS (INSERT INTO aft_x VALUES(1) RETURNING id), y AS (INSERT INTO aft_y VALUES(2) RETURNING id) SELECT x.id AS x,y.id AS y FROM x,y",[]string{"x","y"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)}}))
		s:=fixture.Select("SELECT name,level,id,n FROM aft_events",[]string{"name","level","id","n"},[]string{"TEXT","TEXT","INT4","INT8"},[][]any{{"aft_x","ROW",int64(1),int64(2)},{"aft_y","ROW",int64(2),int64(2)},{"aft_x","STATEMENT",nil,int64(2)},{"aft_y","STATEMENT",nil,int64(2)}});s.Bag=true;run(s)
		userOperatorExec(t,p,b,c,"TRUNCATE aft_events")
		run(fixture.Select("WITH x AS (INSERT INTO aft_x VALUES(5) RETURNING id), y AS (INSERT INTO aft_x VALUES(6) RETURNING id) SELECT x.id AS x,y.id AS y FROM x,y",[]string{"x","y"},[]string{"INT4","INT4"},[][]any{{int64(5),int64(6)}}))
		run(fixture.Select("SELECT level,count(*) AS n FROM aft_events GROUP BY level ORDER BY level",[]string{"level","n"},[]string{"TEXT","INT8"},[][]any{{"ROW",int64(2)},{"STATEMENT",int64(1)}}))
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_cwhen(id integer)",
			"CREATE TABLE aft_cwhen_eval(n bigint)",
			"CREATE TABLE aft_cwhen_rows(id integer,n bigint)",
			"CREATE TABLE aft_cwhen_stat(name text)",
			"CREATE FUNCTION aft_cwhen_condition() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$DECLARE n bigint; BEGIN SELECT count(*) INTO n FROM aft_cwhen; INSERT INTO aft_cwhen_eval VALUES(n); RETURN n=1; END$$",
			"CREATE FUNCTION aft_cwhen_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_cwhen_rows VALUES(NEW.id,(SELECT count(*) FROM aft_cwhen)); RETURN NULL; END$$",
			"CREATE FUNCTION aft_cwhen_statement() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_cwhen_stat VALUES(TG_NAME); RETURN NULL; END$$",
			"CREATE TRIGGER r AFTER INSERT ON aft_cwhen FOR EACH ROW EXECUTE FUNCTION aft_cwhen_row()",
			"CREATE TRIGGER s AFTER INSERT ON aft_cwhen WHEN(aft_cwhen_condition()) EXECUTE FUNCTION aft_cwhen_statement()",
			"CREATE TRIGGER always AFTER INSERT ON aft_cwhen EXECUTE FUNCTION aft_cwhen_statement()")
		run(fixture.Select("WITH x AS (INSERT INTO aft_cwhen VALUES(1) RETURNING id), y AS (INSERT INTO aft_cwhen VALUES(2) RETURNING id) SELECT x.id AS x,y.id AS y FROM x,y",[]string{"x","y"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)}}))
		run(fixture.Select("SELECT n FROM aft_cwhen_eval ORDER BY n",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)},{int64(2)}}))
		run(fixture.Select("SELECT name FROM aft_cwhen_stat",[]string{"name"},[]string{"TEXT"},[][]any{{"always"}}))
		run(fixture.Select("SELECT id,n FROM aft_cwhen_rows ORDER BY id",[]string{"id","n"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)},{int64(2),int64(2)}}))
		for _, q := range []string{
			"WITH x AS (INSERT INTO aft_cwhen VALUES(1)), y AS (INSERT INTO aft_cwhen VALUES(2)) SELECT 7 AS n",
			"WITH x AS (INSERT INTO aft_cwhen VALUES(1) RETURNING id), y AS (INSERT INTO aft_cwhen VALUES(2)) SELECT 7 AS n",
			"WITH x AS (INSERT INTO aft_cwhen VALUES(1)), y AS (INSERT INTO aft_cwhen VALUES(2) RETURNING id) SELECT y.id AS n FROM y",
		} {
			userOperatorExec(t,p,b,c,"TRUNCATE aft_cwhen,aft_cwhen_eval,aft_cwhen_rows,aft_cwhen_stat")
			n:=int64(7);if strings.Contains(q,"FROM y") {n=2}
			run(fixture.Select(q,[]string{"n"},[]string{"INT4"},[][]any{{n}}))
			run(fixture.Select("SELECT n FROM aft_cwhen_eval ORDER BY n",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)},{int64(2)}}))
			run(fixture.Select("SELECT name FROM aft_cwhen_stat",[]string{"name"},[]string{"TEXT"},[][]any{{"always"}}))
			run(fixture.Select("SELECT id,n FROM aft_cwhen_rows ORDER BY id",[]string{"id","n"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)},{int64(2),int64(2)}}))
		}
		userOperatorExec(t,p,b,c,"TRUNCATE aft_cwhen,aft_cwhen_eval,aft_cwhen_rows,aft_cwhen_stat")
		run(fixture.Select("WITH x AS (INSERT INTO aft_cwhen SELECT 1 WHERE false RETURNING id), y AS (INSERT INTO aft_cwhen VALUES(2)) SELECT x.id FROM x",[]string{"id"},[]string{"INT4"},nil))
		run(fixture.Select("SELECT n FROM aft_cwhen_eval ORDER BY n",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)},{int64(1)}}))
		s=fixture.Select("SELECT name FROM aft_cwhen_stat",[]string{"name"},[]string{"TEXT"},[][]any{{"always"},{"s"}});s.Bag=true;run(s)
		run(fixture.Select("SELECT id,n FROM aft_cwhen_rows",[]string{"id","n"},[]string{"INT4","INT8"},[][]any{{int64(2),int64(1)}}))
		userOperatorExec(t,p,b,c,
			"TRUNCATE aft_cwhen,aft_cwhen_eval,aft_cwhen_rows,aft_cwhen_stat",
			"CREATE FUNCTION aft_cwhen_error() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO aft_cwhen_eval VALUES(99); PERFORM 1/0; RETURN true; END$$",
			"CREATE TRIGGER z AFTER INSERT ON aft_cwhen WHEN(aft_cwhen_error()) EXECUTE FUNCTION aft_cwhen_statement()")
		for _, q := range []string{
			"WITH x AS (INSERT INTO aft_cwhen VALUES(1)), y AS (INSERT INTO aft_cwhen VALUES(2)) SELECT 7",
			"WITH x AS (INSERT INTO aft_cwhen VALUES(1)), y AS (INSERT INTO aft_cwhen VALUES(2) RETURNING id) SELECT y.id FROM y",
			"WITH x AS (INSERT INTO aft_cwhen VALUES(1) RETURNING id), y AS (INSERT INTO aft_cwhen VALUES(2) RETURNING id) SELECT x.id,y.id FROM x,y",
		} {
			diagnostic:=fixture.ControlsReferenceDiagnostic("22012","division by zero")
			diagnostic.Context="SQL statement \"SELECT 1/0\"\nPL/pgSQL function aft_cwhen_error() line 1 at PERFORM"
			failure:=fixture.ErrorDiagnostic(q,diagnostic);failure.SelectRows=true;run(failure)
			for _, table := range []string{"aft_cwhen","aft_cwhen_eval","aft_cwhen_rows","aft_cwhen_stat"} {
				run(fixture.Select("SELECT count(*) AS n FROM "+table,[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
			}
		}
		userOperatorExec(t,p,b,c,"TRUNCATE aft_events","CREATE TABLE aft_caught(id integer,n integer)","CREATE FUNCTION aft_outer() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE caught integer:=0; BEGIN BEGIN INSERT INTO aft_y VALUES(10),(11); PERFORM 1/0; EXCEPTION WHEN division_by_zero THEN caught:=caught+1; END; INSERT INTO aft_caught VALUES(NEW.id,caught); INSERT INTO aft_y VALUES(NEW.id+10),(NEW.id+20); INSERT INTO aft_events VALUES('resume',TG_LEVEL,NEW.id,(SELECT count(*) FROM aft_events)); RETURN NULL; END$$","CREATE OR REPLACE TRIGGER r AFTER INSERT ON aft_x FOR EACH ROW EXECUTE FUNCTION aft_outer()")
		run(fixture.Exec("INSERT INTO aft_x VALUES(3),(4)",2))
		run(fixture.Select("SELECT id FROM aft_y ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(2)},{int64(13)},{int64(14)},{int64(23)},{int64(24)}}))
		run(fixture.Select("SELECT id,n FROM aft_events WHERE name='resume' ORDER BY id",[]string{"id","n"},[]string{"INT4","INT8"},[][]any{{int64(3),int64(3)},{int64(4),int64(7)}}))
		run(fixture.Select("SELECT count(*) AS n FROM aft_events WHERE name='aft_x' AND level='STATEMENT'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}}))
		run(fixture.Select("SELECT id,n FROM aft_caught ORDER BY id",[]string{"id","n"},[]string{"INT4","INT4"},[][]any{{int64(3),int64(1)},{int64(4),int64(1)}}))
	})
}

func TestReferenceTriggerAfterTruncateTransaction(t *testing.T) {
	afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool){
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_trtx(id integer PRIMARY KEY,v integer)",
			"CREATE TABLE aft_trtx_log(n bigint)",
			"CREATE FUNCTION aft_trtx_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_trtx_log SELECT count(*) FROM aft_trtx; RETURN NULL; END$$",
			"CREATE TRIGGER a AFTER TRUNCATE ON aft_trtx EXECUTE FUNCTION aft_trtx_log()")
		run:=func(s fixture.Step){afterStep(t,p,b,c,prepared,s)}
		utility:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		utility(fixture.Exec("BEGIN",0))
		run(fixture.Exec("INSERT INTO aft_trtx VALUES(1,10),(2,20),(3,30)",3))
		run(fixture.Exec("UPDATE aft_trtx SET v=v+1 WHERE id=1",1))
		run(fixture.Exec("DELETE FROM aft_trtx WHERE id=2",1))
		utility(fixture.Exec("TRUNCATE aft_trtx",0))
		run(fixture.Select("SELECT n FROM aft_trtx_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO aft_trtx VALUES(4,40)",1))
		utility(fixture.Exec("TRUNCATE aft_trtx",0))
		run(fixture.Select("SELECT n FROM aft_trtx_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)},{int64(0)}}))
		run(fixture.Select("SELECT id,v FROM aft_trtx",[]string{"id","v"},[]string{"INT4","INT4"},nil))
		utility(fixture.Exec("ROLLBACK",0))
		run(fixture.Select("SELECT count(*) AS n FROM aft_trtx_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION aft_trtx_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
			"CREATE TRIGGER z AFTER TRUNCATE ON aft_trtx EXECUTE FUNCTION aft_trtx_fail()")
		utility(fixture.Exec("BEGIN",0))
		run(fixture.Exec("INSERT INTO aft_trtx VALUES(1,10),(2,20),(3,30)",3))
		utility(fixture.Exec("SAVEPOINT before_truncate",0))
		d:=fixture.ControlsReferenceDiagnostic("22012","division by zero")
		d.Context="SQL statement \"SELECT 1/0\"\nPL/pgSQL function aft_trtx_fail() line 1 at PERFORM"
		utility(fixture.ErrorDiagnostic("TRUNCATE aft_trtx",d))
		utility(fixture.Exec("ROLLBACK TO SAVEPOINT before_truncate",0))
		run(fixture.Select("SELECT id,v FROM aft_trtx ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}}))
		run(fixture.Select("SELECT count(*) AS n FROM aft_trtx_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		utility(fixture.Exec("ROLLBACK",0))
	})
}
