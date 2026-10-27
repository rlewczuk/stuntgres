package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func afterRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared { name = "prepared" }
		t.Run(name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) { body(t, p, b, c, prepared) })
		})
	}
}
func afterStep(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, step fixture.Step) {
	t.Helper()
	if prepared { step = fixture.Prepared(step) }
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
}
func afterSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE aft(id integer PRIMARY KEY,v integer,g integer GENERATED ALWAYS AS(v*2) STORED,u integer DEFAULT 0)",
		"CREATE TABLE aft_log(seq integer GENERATED ALWAYS AS IDENTITY,op text,level text,name text,id integer,ov integer,v integer,g integer,n bigint)",
		"CREATE FUNCTION aft_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
		"CREATE FUNCTION aft_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_log(op,level,name,id,ov,v,g,n) VALUES(TG_OP,TG_LEVEL,TG_NAME,COALESCE(NEW.id,OLD.id),OLD.v,NEW.v,NEW.g,(SELECT count(*) FROM aft)); IF TG_OP<>'DELETE' THEN NEW.v:=999; END IF; RETURN NULL; END$$",
		"CREATE FUNCTION aft_statement() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_log(op,level,name,n) VALUES(TG_OP,TG_LEVEL,TG_NAME,(SELECT count(*) FROM aft_log)); RETURN NULL; END$$")
}
func TestReferenceTriggerAfterRows(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		afterSetup(t,p,b,c)
		userOperatorExec(t,p,b,c,
			"CREATE TRIGGER before_row BEFORE INSERT ON aft FOR EACH ROW EXECUTE FUNCTION aft_before()",
			"CREATE TRIGGER row_event AFTER INSERT OR UPDATE OR DELETE ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()")
		run := func(s fixture.Step) { afterStep(t,p,b,c,prepared,s) }
		s := fixture.Select("INSERT INTO aft(id,v) VALUES(1,10),(2,20) RETURNING id,v,g", []string{"id","v","g"}, []string{"INT4","INT4","INT4"}, [][]any{{int64(1),int64(11),int64(22)},{int64(2),int64(21),int64(42)}})
		s.Bag=true; run(s)
		run(fixture.Exec("UPDATE aft SET v=v+10",2))
		run(fixture.Exec("DELETE FROM aft",2))
		run(fixture.Select("SELECT op,id,ov,v,g,n FROM aft_log ORDER BY op,id", []string{"op","id","ov","v","g","n"}, []string{"TEXT","INT4","INT4","INT4","INT4","INT8"}, [][]any{
			{"DELETE",int64(1),int64(21),nil,nil,int64(0)},{"DELETE",int64(2),int64(31),nil,nil,int64(0)},
			{"INSERT",int64(1),nil,int64(11),int64(22),int64(2)},{"INSERT",int64(2),nil,int64(21),int64(42),int64(2)},
			{"UPDATE",int64(1),int64(11),int64(21),int64(42),int64(2)},{"UPDATE",int64(2),int64(21),int64(31),int64(62),int64(2)},
		}))
	})
}
func TestReferenceTriggerAfterOrdering(t *testing.T) {
	afterRoutes(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool) {
		afterSetup(t,p,b,c)
		userOperatorExec(t,p,b,c,
			"CREATE TRIGGER z AFTER INSERT ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER a AFTER INSERT ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER s AFTER INSERT ON aft EXECUTE FUNCTION aft_statement()")
		run := func(s fixture.Step) { afterStep(t,p,b,c,prepared,s) }
		run(fixture.Exec("INSERT INTO aft(id,v) VALUES(1,10),(2,20)",2))
		run(fixture.Select("SELECT id,name,n FROM aft_log WHERE level='ROW' ORDER BY id,seq", []string{"id","name","n"}, []string{"INT4","TEXT","INT8"}, [][]any{{int64(1),"a",int64(2)},{int64(1),"z",int64(2)},{int64(2),"a",int64(2)},{int64(2),"z",int64(2)}}))
		run(fixture.Select("SELECT name,n FROM aft_log WHERE level='STATEMENT'", []string{"name","n"}, []string{"TEXT","INT8"}, [][]any{{"s",int64(4)}}))
	})
}
func TestReferenceTriggerAfterReturns(t *testing.T) {
	afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool) {
		afterSetup(t,p,b,c)
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION aft_mutate() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=999; RETURN NEW; END$$",
			"CREATE FUNCTION aft_odd() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN ROW(1); END$$",
			"CREATE TRIGGER a AFTER INSERT OR UPDATE ON aft FOR EACH ROW EXECUTE FUNCTION aft_mutate()",
			"CREATE TRIGGER z AFTER INSERT OR UPDATE OR DELETE ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER statement AFTER INSERT ON aft EXECUTE FUNCTION aft_odd()")
		run := func(s fixture.Step) { afterStep(t,p,b,c,prepared,s) }
		run(triggerRows("INSERT INTO aft(id,v) VALUES(1,10) RETURNING id,v",[]any{int64(1),int64(10)}))
		run(triggerRows("UPDATE aft SET v=20 RETURNING id,v",[]any{int64(1),int64(20)}))
		run(triggerRows("DELETE FROM aft RETURNING id,v",[]any{int64(1),int64(20)}))
		run(fixture.Select("SELECT op,v FROM aft_log ORDER BY seq",[]string{"op","v"},[]string{"TEXT","INT4"},[][]any{{"INSERT",int64(10)},{"UPDATE",int64(20)},{"DELETE",nil}}))
		userOperatorExec(t,p,b,c,"CREATE OR REPLACE FUNCTION aft_odd() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN ROW(1/0); END$$")
		s:=fixture.Error("INSERT INTO aft(id,v) VALUES(2,30) RETURNING id,v","22012");s.SelectRows=true;run(s)
		run(triggerRows("SELECT id,v FROM aft"))
		run(fixture.Select("SELECT count(*) AS n FROM aft_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(3)}}))
	})
}
func TestReferenceTriggerAfterZeroRows(t *testing.T) {
	afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool) {
		afterSetup(t,p,b,c)
		userOperatorExec(t,p,b,c,
			"CREATE TRIGGER r AFTER INSERT OR UPDATE OR DELETE ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER s AFTER INSERT OR UPDATE OR DELETE ON aft EXECUTE FUNCTION aft_statement()")
		run := func(s fixture.Step) { afterStep(t,p,b,c,prepared,s) }
		q := "UPDATE aft SET v=v WHERE id=$1 RETURNING id,v"
		held,err:=c.PrepareContext(p.Ctx,q); if err!=nil { t.Fatal(err) }; defer held.Close()
		run(fixture.Select("SELECT count(*) AS n FROM aft_log",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO aft(id,v) SELECT 1,2 WHERE false",0))
		s:=triggerRows(q); s.Args=[]any{999}; fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},b.Recorder,s)
		run(triggerRows("DELETE FROM aft WHERE id=999 RETURNING id,v"))
		userOperatorExec(t,p,b,c,"CREATE FUNCTION aft_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$","CREATE TRIGGER before_row BEFORE INSERT ON aft FOR EACH ROW EXECUTE FUNCTION aft_suppress()")
		run(fixture.Exec("INSERT INTO aft(id,v) VALUES(1,2),(2,3)",0))
		run(fixture.Select("SELECT op,level,n FROM aft_log ORDER BY seq",[]string{"op","level","n"},[]string{"TEXT","TEXT","INT8"},[][]any{{"INSERT","STATEMENT",int64(0)},{"UPDATE","STATEMENT",int64(1)},{"DELETE","STATEMENT",int64(2)},{"INSERT","STATEMENT",int64(3)}}))
	})
}
func TestReferenceTriggerAfterWhen(t *testing.T) {
	afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool) {
		afterSetup(t,p,b,c)
		userOperatorExec(t,p,b,c,
			"CREATE TRIGGER a AFTER INSERT ON aft FOR EACH ROW WHEN(NEW.g=22) EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER f AFTER INSERT ON aft FOR EACH ROW WHEN(false) EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER n AFTER INSERT ON aft FOR EACH ROW WHEN(NULL) EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER v AFTER UPDATE OF v ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()",
			"CREATE TRIGGER g AFTER UPDATE OF g ON aft FOR EACH ROW EXECUTE FUNCTION aft_row()")
		run := func(s fixture.Step) { afterStep(t,p,b,c,prepared,s) }
		run(fixture.Exec("INSERT INTO aft(id,v) VALUES(1,11),(2,21)",2))
		run(fixture.Exec("UPDATE aft SET u=u",2))
		run(fixture.Exec("UPDATE aft SET v=v",2))
		run(fixture.Select("SELECT name,id FROM aft_log ORDER BY name,id",[]string{"name","id"},[]string{"TEXT","INT4"},[][]any{{"a",int64(1)},{"g",int64(1)},{"g",int64(2)},{"v",int64(1)},{"v",int64(2)}}))
		// The volatile WHEN helper sees the accepted prefix, unlike the body.
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION aft_first(integer) RETURNS boolean LANGUAGE SQL VOLATILE AS 'SELECT count(*)=1 FROM aft WHERE id=$1 OR id<=$1'",
			"CREATE TRIGGER w AFTER INSERT ON aft FOR EACH ROW WHEN(aft_first(NEW.id)) EXECUTE FUNCTION aft_row()",
			"TRUNCATE aft_log")
		run(fixture.Exec("DELETE FROM aft",2))
		userOperatorExec(t,p,b,c,"TRUNCATE aft_log")
		run(fixture.Exec("INSERT INTO aft(id,v) VALUES(3,10),(4,20)",2))
		run(fixture.Select("SELECT name,id,n FROM aft_log ORDER BY seq",[]string{"name","id","n"},[]string{"TEXT","INT4","INT8"},[][]any{{"w",int64(3),int64(2)}}))
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_when(id integer)",
			"CREATE TABLE aft_when_rows(id integer)",
			"CREATE TABLE aft_when_eval(n bigint,r bigint)",
			"CREATE TABLE aft_when_stat(name text)",
			"CREATE FUNCTION aft_when_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_when_rows VALUES(NEW.id); RETURN NULL; END$$",
			"CREATE FUNCTION aft_when_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_when_stat VALUES(TG_NAME); RETURN NULL; END$$",
			"CREATE FUNCTION aft_condition() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO aft_when_eval VALUES((SELECT count(*) FROM aft_when),(SELECT count(*) FROM aft_when_rows)); RETURN (SELECT count(*) FROM aft_when)=2 AND (SELECT count(*) FROM aft_when_rows)=2; END$$",
			"CREATE TRIGGER r AFTER INSERT ON aft_when FOR EACH ROW EXECUTE FUNCTION aft_when_row()",
			"CREATE TRIGGER s AFTER INSERT ON aft_when WHEN(aft_condition()) EXECUTE FUNCTION aft_when_body()",
			"CREATE TRIGGER f AFTER INSERT ON aft_when WHEN(false) EXECUTE FUNCTION aft_when_body()",
			"CREATE TRIGGER n AFTER INSERT ON aft_when WHEN(NULL) EXECUTE FUNCTION aft_when_body()")
		run(fixture.Exec("INSERT INTO aft_when VALUES(1),(2)",2))
		run(fixture.Exec("INSERT INTO aft_when SELECT 3 WHERE false",0))
		run(fixture.Select("SELECT n,r FROM aft_when_eval",[]string{"n","r"},[]string{"INT8","INT8"},[][]any{{int64(2),int64(0)},{int64(2),int64(2)}}))
		run(fixture.Select("SELECT name FROM aft_when_stat",[]string{"name"},[]string{"TEXT"},[][]any{{"s"}}))
		userOperatorExec(t,p,b,c,
			"CREATE FUNCTION aft_condition_error() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN RETURN 1/0=0; END$$",
			"CREATE TRIGGER z AFTER INSERT ON aft_when WHEN(aft_condition_error()) EXECUTE FUNCTION aft_when_body()")
		run(fixture.Error("INSERT INTO aft_when VALUES(3)","22012"))
		run(fixture.Select("SELECT id FROM aft_when ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		run(fixture.Select("SELECT n,r FROM aft_when_eval",[]string{"n","r"},[]string{"INT8","INT8"},[][]any{{int64(2),int64(0)},{int64(2),int64(2)}}))
		run(fixture.Select("SELECT name FROM aft_when_stat",[]string{"name"},[]string{"TEXT"},[][]any{{"s"}}))
		run(fixture.Select("SELECT id FROM aft_when_rows ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
	})
}
