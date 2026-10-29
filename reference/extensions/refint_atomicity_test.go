package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRefintAtomicity(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		for _,q := range []string{"CREATE TABLE p(id int)","CREATE TABLE c1(pid int)","CREATE TABLE c2(pid int NOT NULL)"} { run(fixture.Exec(q,0)) }
		for _,q := range []string{"INSERT INTO p VALUES(1)","INSERT INTO c1 VALUES(1)","INSERT INTO c2 VALUES(1)"} { run(fixture.Exec(q,1)) }
		run(fixture.Exec("CREATE TRIGGER atomic_guard AFTER DELETE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('2','s','id','c1','pid','c2','pid')",0))
		baseline := func() {
			for _,table := range []string{"p","c1","c2"} {
				column := "pid"; if table=="p" { column="id" }
				run(fixture.Select("SELECT "+column+" FROM "+table,[]string{column},[]string{"INT4"},[][]any{{int64(1)}}))
			}
		}
		// Full PostgreSQL 18.6 diagnostic observed in tests-1, including empty fields.
		for _,mode := range []string{"autocommit","transaction","savepoint"} {
			t.Run(mode,func(t *testing.T) {
				if mode!="autocommit" { run(fixture.Exec("BEGIN",0)) }
				if mode=="savepoint" { run(fixture.Exec("SAVEPOINT before_action",0)) }
				d := refintError("23502", `null value in column "pid" of relation "c2" violates not-null constraint`)
				d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (null).", "public", "c2", "pid"
				d.Context = `SQL statement "update c2 set pid = null where pid = $1 "`
				run(fixture.ErrorDiagnostic("DELETE FROM p WHERE id=1", d))
				refintNotices(t,notices,"atomic_guard: 1 tuple(s) of c1 are set to null")
				if mode!="autocommit" {
					fixture.RunStep(t,p.Ctx,c,r,fixture.ErrorDiagnostic("SELECT id FROM p",refintError("25P02","current transaction is aborted, commands ignored until end of transaction block")))
					if mode=="savepoint" { run(fixture.Exec("ROLLBACK TO SAVEPOINT before_action",0)); baseline(); run(fixture.Exec("COMMIT",0)) } else { run(fixture.Exec("ROLLBACK",0)) }
				}
				baseline()
			})
		}
	})
}

func TestReferenceRefintVisibility(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE self_key(id int,parent int)",0))
		run(fixture.Exec("CREATE TRIGGER self_guard AFTER INSERT ON self_key FOR EACH ROW EXECUTE FUNCTION check_primary_key('parent','self_key','id')",0))
		run(fixture.Exec("INSERT INTO self_key VALUES(1,2),(2,1)",2))
		run(fixture.Select("SELECT id,parent FROM self_key ORDER BY id",[]string{"id","parent"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)},{int64(2),int64(1)}}))
		for _,q := range []string{"CREATE TABLE p(id int)","CREATE TABLE c(id int,pid int)","CREATE TABLE audit(id int)"} { run(fixture.Exec(q,0)) }
		run(fixture.Exec("INSERT INTO p VALUES(1)",1)); run(fixture.Exec("INSERT INTO c VALUES(10,1),(11,1)",2))
		run(fixture.Exec("CREATE FUNCTION child_before() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.id=10 THEN RETURN NULL; END IF; RETURN OLD; END $$",0))
		run(fixture.Exec("CREATE FUNCTION child_after() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO audit VALUES(OLD.id); RETURN NULL; END $$",0))
		run(fixture.Exec("CREATE TRIGGER child_before BEFORE DELETE ON c FOR EACH ROW EXECUTE FUNCTION child_before()",0))
		run(fixture.Exec("CREATE TRIGGER child_after AFTER DELETE ON c FOR EACH ROW EXECUTE FUNCTION child_after()",0))
		run(fixture.Exec("CREATE TRIGGER parent_guard AFTER DELETE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','c','id','c','pid')",0))
		run(fixture.Exec("DELETE FROM p",1)); refintNotices(t,notices,"parent_guard: 1 tuple(s) of c are deleted")
		run(fixture.Select("SELECT id,pid FROM c ORDER BY id",[]string{"id","pid"},[]string{"INT4","INT4"},[][]any{{int64(10),int64(1)}}))
		run(fixture.Select("SELECT id FROM audit ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(11)}}))
		// Generated action SQL is not ONLY: descendants participate normally,
		// without inheriting the root child's row triggers.
		run(fixture.Exec("CREATE TABLE c_descendant() INHERITS(c)",0))
		run(fixture.Exec("INSERT INTO p VALUES(2)",1))
		run(fixture.Exec("INSERT INTO c_descendant VALUES(12,2)",1))
		run(fixture.Exec("DELETE FROM p WHERE id=2",1))
		refintNotices(t,notices,"parent_guard: 1 tuple(s) of c are deleted")
		run(fixture.Select("SELECT id,pid FROM c ORDER BY id",[]string{"id","pid"},[]string{"INT4","INT4"},[][]any{{int64(10),int64(1)}}))
		run(fixture.Select("SELECT id,pid FROM c_descendant ORDER BY id",[]string{"id","pid"},[]string{"INT4","INT4"},nil))
		run(fixture.Select("SELECT id FROM audit ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(11)}}))
	})
}

func TestReferenceRefintPreparationOrder(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE p(id int)",0)); run(fixture.Exec("INSERT INTO p VALUES(1)",1))
		run(fixture.Exec("CREATE TABLE c(pid int)",0)); run(fixture.Exec("INSERT INTO c VALUES(1)",1))
		run(fixture.Exec("CREATE TRIGGER prepare_guard AFTER DELETE OR UPDATE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('2','c','id','c','pid','refint_absent','pid')",0))
		for _,q := range []string{"UPDATE p SET id=id","DELETE FROM p"} {
			t.Run(q,func(t *testing.T) {
				d := refintError("42P01", `relation "refint_absent" does not exist`)
				d.InternalPosition, d.InternalQuery = "13", "delete from refint_absent where pid = $1 "
				if q == "UPDATE p SET id=id" { d.InternalPosition, d.InternalQuery = "8", "update refint_absent set  pid = '1'  where pid = $1 " }
				run(fixture.ErrorDiagnostic(q, d))
			})
			refintNotices(t,notices)
			run(fixture.Select("SELECT pid FROM c",[]string{"pid"},[]string{"INT4"},[][]any{{int64(1)}}))
			run(fixture.Select("SELECT id FROM p",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
		}
	})
}
