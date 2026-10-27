package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerAfterProducers(t *testing.T) {
	afterRoutes(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool){
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_parent(id integer PRIMARY KEY)",
			"CREATE TABLE aft_child(id integer REFERENCES aft_parent(id) ON UPDATE CASCADE ON DELETE CASCADE)",
			"CREATE TABLE aft_prodlog(rel text,op text,level text,id integer)",
			"CREATE FUNCTION aft_prod() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_prodlog VALUES(TG_TABLE_NAME,TG_OP,TG_LEVEL,COALESCE(NEW.id,OLD.id)); RETURN NULL; END$$",
			"CREATE TRIGGER r AFTER INSERT OR UPDATE OR DELETE ON aft_child FOR EACH ROW EXECUTE FUNCTION aft_prod()",
			"CREATE TRIGGER s AFTER INSERT ON aft_child EXECUTE FUNCTION aft_prod()")
		run:=func(s fixture.Step){afterStep(t,p,b,c,prepared,s)}
		run(fixture.Exec("INSERT INTO aft_parent VALUES(1),(2),(3),(4)",4))
		run(fixture.Exec("INSERT INTO aft_child SELECT id FROM aft_parent WHERE id<3",2))
		copyDB:=p.Native
		if b.PG { copyDB=p.CopyPostgres }
		copyConn,err:=copyDB.Conn(p.Ctx);if err!=nil {t.Fatal(err)}
		result,err:=fixture.CopyFrom(p.Ctx,copyConn,b.PG,"COPY aft_child(id) FROM STDIN",strings.NewReader("3\n4\n"));copyConn.Close()
		if err!=nil || result.Count!=2 {t.Fatal(result,err)}
		run(fixture.Select("SELECT level,count(*) AS n FROM aft_prodlog GROUP BY level ORDER BY level",[]string{"level","n"},[]string{"TEXT","INT8"},[][]any{{"ROW",int64(4)},{"STATEMENT",int64(2)}}))
		run(fixture.Exec("UPDATE aft_parent SET id=id+10",4))
		run(fixture.Exec("DELETE FROM aft_parent",4))
		run(fixture.Select("SELECT op,count(*) AS n FROM aft_prodlog WHERE level='ROW' GROUP BY op ORDER BY op",[]string{"op","n"},[]string{"TEXT","INT8"},[][]any{{"DELETE",int64(4)},{"INSERT",int64(4)},{"UPDATE",int64(4)}}))
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_part(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE aft_lo PARTITION OF aft_part FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE aft_hi PARTITION OF aft_part FOR VALUES FROM(10) TO(20)",
			"CREATE TRIGGER r AFTER INSERT OR UPDATE OR DELETE ON aft_part FOR EACH ROW EXECUTE FUNCTION aft_prod()",
			"CREATE TRIGGER s AFTER INSERT OR UPDATE ON aft_part EXECUTE FUNCTION aft_prod()",
			"CREATE TRIGGER child_statement AFTER INSERT ON aft_lo EXECUTE FUNCTION aft_prod()",
			"TRUNCATE aft_prodlog")
		run(fixture.Exec("INSERT INTO aft_part VALUES(1),(11)",2))
		run(fixture.Exec("UPDATE aft_part SET id=12 WHERE id=1",1))
		run(fixture.Exec("INSERT INTO aft_lo VALUES(2)",1))
		s:=fixture.Select("SELECT rel,op,level,id FROM aft_prodlog",[]string{"rel","op","level","id"},[]string{"TEXT","TEXT","TEXT","INT4"},[][]any{
			{"aft_lo","INSERT","ROW",int64(1)},{"aft_hi","INSERT","ROW",int64(11)},{"aft_part","INSERT","STATEMENT",nil},
			{"aft_lo","DELETE","ROW",int64(1)},{"aft_hi","INSERT","ROW",int64(12)},{"aft_part","UPDATE","STATEMENT",nil},
			{"aft_lo","INSERT","ROW",int64(2)},{"aft_lo","INSERT","STATEMENT",nil},
		});s.Bag=true;run(s)
	})
}
func TestReferenceTriggerAfterPartitionDefinitions(t *testing.T) {
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		userOperatorExec(t,p,b,c,
			"CREATE TABLE aft_part(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE aft_lo PARTITION OF aft_part FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE aft_deflog(rel text,level text)",
			"CREATE FUNCTION aft_def() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_deflog VALUES(TG_TABLE_NAME,TG_LEVEL); RETURN NULL; END$$",
			"CREATE TRIGGER local AFTER INSERT ON aft_lo EXECUTE FUNCTION aft_def()",
			"CREATE TRIGGER local AFTER INSERT ON aft_part EXECUTE FUNCTION aft_def()",
			"CREATE TRIGGER changing AFTER INSERT ON aft_part FOR EACH ROW EXECUTE FUNCTION aft_def()")
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.Exec("INSERT INTO aft_part VALUES(1)",1))
		userOperatorExec(t,p,b,c,"CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_part EXECUTE FUNCTION aft_def()")
		run(fixture.Prepared(fixture.Exec("INSERT INTO aft_part VALUES(2)",1)))
		userOperatorExec(t,p,b,c,"CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_part FOR EACH ROW EXECUTE FUNCTION aft_def()")
		run(fixture.Exec("INSERT INTO aft_part VALUES(3)",1))
		run(fixture.Select("SELECT rel,level,count(*) AS n FROM aft_deflog GROUP BY rel,level ORDER BY rel,level",[]string{"rel","level","n"},[]string{"TEXT","TEXT","INT8"},[][]any{{"aft_lo","ROW",int64(3)},{"aft_part","STATEMENT",int64(4)}}))
		run(fixture.Select("SELECT trigger_name,action_orientation FROM information_schema.triggers WHERE event_object_table='aft_lo' ORDER BY trigger_name",[]string{"trigger_name","action_orientation"},[]string{"NAME","VARCHAR"},[][]any{{"changing","ROW"},{"local","STATEMENT"}}))
		userOperatorExec(t,p,b,c,"CREATE TABLE aft_hi(id integer)","CREATE TRIGGER local AFTER INSERT ON aft_hi EXECUTE FUNCTION aft_def()","ALTER TABLE aft_part ATTACH PARTITION aft_hi FOR VALUES FROM(10) TO(20)")
		run(fixture.Exec("INSERT INTO aft_part VALUES(11)",1))
		userOperatorExec(t,p,b,c,"ALTER TABLE aft_part DETACH PARTITION aft_hi")
		run(fixture.Exec("INSERT INTO aft_hi VALUES(12)",1))
		run(fixture.Select("SELECT trigger_name,action_orientation FROM information_schema.triggers WHERE event_object_table='aft_hi'",[]string{"trigger_name","action_orientation"},[]string{"NAME","VARCHAR"},[][]any{{"local","STATEMENT"}}))
	})
}

func TestReferenceTriggerAfterReferentialCommands(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE aft_ri_parent(id integer PRIMARY KEY)",
			"CREATE TABLE aft_ri_child(id integer PRIMARY KEY,pid integer REFERENCES aft_ri_parent ON UPDATE CASCADE ON DELETE CASCADE)",
			"CREATE TABLE aft_ri_log(op text,level text,id integer,n bigint,rows_seen bigint)",
			"CREATE TABLE aft_ri_eval(n bigint)",
			"CREATE FUNCTION aft_ri_condition() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO aft_ri_eval SELECT count(*) FROM aft_ri_child WHERE pid>10; RETURN true; END$$",
			"CREATE FUNCTION aft_ri_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_ri_log VALUES(TG_OP,TG_LEVEL,COALESCE(NEW.id,OLD.id),(SELECT count(*) FROM aft_ri_child WHERE pid>10),(SELECT count(*) FROM aft_ri_log WHERE level='ROW')); RETURN NULL; END$$",
			"CREATE TRIGGER r AFTER UPDATE OR DELETE ON aft_ri_child FOR EACH ROW EXECUTE FUNCTION aft_ri_log()",
			"CREATE TRIGGER s AFTER UPDATE OR DELETE ON aft_ri_child WHEN(aft_ri_condition()) EXECUTE FUNCTION aft_ri_log()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		log := func(expected [][]any) {
			s := fixture.Select("SELECT op,level,n,rows_seen FROM aft_ri_log", []string{"op", "level", "n", "rows_seen"}, []string{"TEXT", "TEXT", "INT8", "INT8"}, expected)
			s.Bag = true
			run(s)
		}
		eval:=func(expected [][]any) {
			run(fixture.Select("SELECT n FROM aft_ri_eval ORDER BY n",[]string{"n"},[]string{"INT8"},expected))
		}
		run(fixture.Exec("INSERT INTO aft_ri_parent VALUES(1),(2),(3)", 3))
		run(fixture.Exec("INSERT INTO aft_ri_child VALUES(11,1),(12,1),(21,2),(22,2)", 4))
		run(fixture.Exec("UPDATE aft_ri_parent SET id=id+10 WHERE id<3", 2))
		log([][]any{
			{"UPDATE", "ROW", int64(4), int64(0)}, {"UPDATE", "ROW", int64(4), int64(1)},
			{"UPDATE", "ROW", int64(4), int64(2)}, {"UPDATE", "ROW", int64(4), int64(3)}, {"UPDATE", "STATEMENT", int64(4), int64(4)},
		})
		// Each generated producer qualifies at exhaustion, but its bodies use
		// the outer query queue and see every completed referential action.
		eval([][]any{{int64(2)},{int64(4)}})
		run(fixture.Select("SELECT id,pid FROM aft_ri_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(11)}, {int64(12), int64(11)}, {int64(21), int64(12)}, {int64(22), int64(12)}}))
		userOperatorExec(t, p, b, c, "TRUNCATE aft_ri_log,aft_ri_eval")
		run(fixture.Exec("DELETE FROM aft_ri_parent WHERE id>10", 2))
		log([][]any{
			{"DELETE", "ROW", int64(0), int64(0)}, {"DELETE", "ROW", int64(0), int64(1)},
			{"DELETE", "ROW", int64(0), int64(2)}, {"DELETE", "ROW", int64(0), int64(3)}, {"DELETE", "STATEMENT", int64(0), int64(4)},
		})
		eval([][]any{{int64(0)},{int64(2)}})
		userOperatorExec(t, p, b, c, "TRUNCATE aft_ri_log,aft_ri_eval")
		run(fixture.Exec("UPDATE aft_ri_parent SET id=13 WHERE id=3", 1))
		run(fixture.Exec("DELETE FROM aft_ri_parent WHERE id=13", 1))
		log([][]any{{"UPDATE", "STATEMENT", int64(0), int64(0)}, {"DELETE", "STATEMENT", int64(0), int64(0)}})
		eval([][]any{{int64(0)},{int64(0)}})
		userOperatorExec(t, p, b, c, "TRUNCATE aft_ri_log,aft_ri_eval")
		run(fixture.Exec("UPDATE aft_ri_parent SET id=id+100 WHERE id=999", 0))
		run(fixture.Exec("DELETE FROM aft_ri_parent WHERE id=999", 0))
		log(nil)
		eval(nil)
		// Populated and empty child producers retain statement events in the
		// outer queue; a deferred body failure rolls back all preceding effects.
		run(fixture.Exec("INSERT INTO aft_ri_parent VALUES(1),(2),(3)", 3))
		run(fixture.Exec("INSERT INTO aft_ri_child VALUES(11,1),(12,1),(21,2),(22,2)", 4))
		userOperatorExec(t, p, b, c,
			"TRUNCATE aft_ri_log,aft_ri_eval",
			"CREATE FUNCTION aft_ri_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
			"CREATE TRIGGER z AFTER UPDATE OR DELETE ON aft_ri_child EXECUTE FUNCTION aft_ri_fail()")
		for _, q := range []string{
			"UPDATE aft_ri_parent SET id=id+10 WHERE id<3",
			"DELETE FROM aft_ri_parent WHERE id<3",
			"UPDATE aft_ri_parent SET id=13 WHERE id=3",
			"DELETE FROM aft_ri_parent WHERE id=3",
		} {
			d:=fixture.ControlsReferenceDiagnostic("22012","division by zero")
			d.Context="SQL statement \"SELECT 1/0\"\nPL/pgSQL function aft_ri_fail() line 1 at PERFORM"
			run(fixture.ErrorDiagnostic(q,d))
			run(fixture.Select("SELECT id FROM aft_ri_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			run(fixture.Select("SELECT id,pid FROM aft_ri_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(1)}, {int64(12), int64(1)}, {int64(21), int64(2)}, {int64(22), int64(2)}}))
			log(nil)
			eval(nil)
		}
	})
}
