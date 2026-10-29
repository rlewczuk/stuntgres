package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRefintPrimary(t *testing.T) {
	refintCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE p(a int,b text)", 0))
		// Duplicates and NULL parent keys are not prohibited by this helper.
		run(fixture.Exec("INSERT INTO p VALUES(1,'one'),(1,'one'),(NULL,'null')", 3))
		run(fixture.Exec("CREATE TABLE c(id int,a int,b text)", 0))
		run(fixture.Exec("CREATE TRIGGER child_guard AFTER INSERT OR UPDATE ON c FOR EACH ROW EXECUTE FUNCTION check_primary_key('a','b','p','a','b')", 0))
		run(fixture.Exec("INSERT INTO c VALUES(10,1,'one')", 1))
		d := refintError("09000", "tuple references non-existent key")
		d.Detail = `Trigger "child_guard" found tuple referencing non-existent key in "p".`
		for _, s := range []fixture.Step{
			fixture.ErrorDiagnostic("INSERT INTO c VALUES(11,2,'two')", d),
			fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO c VALUES($1,$2,$3)", d, 11, 2, "two")),
			fixture.ErrorDiagnostic("INSERT INTO c VALUES(20,1,'one'),(21,2,'two')", d),
			fixture.ErrorDiagnostic("UPDATE c SET a=2,b='two' WHERE id=10", d),
		} { run(s) }
		run(fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1,$2,$3)", 1, 12, nil, "missing")))
		run(fixture.Exec("INSERT INTO c VALUES(13,999,NULL)", 1))
		for _, prepared := range []bool{false,true} {
			s := fixture.Select("SELECT id,a,b FROM c ORDER BY id", []string{"id","a","b"}, []string{"INT4","INT4","TEXT"}, [][]any{{int64(10),int64(1),"one"},{int64(12),nil,"missing"},{int64(13),int64(999),nil}})
			s.Prepared = prepared; run(s)
			s = fixture.Select("SELECT id,a,b FROM c WHERE id=999 ORDER BY id", []string{"id","a","b"}, []string{"INT4","INT4","TEXT"}, nil)
			s.Prepared = prepared; run(s)
		}
		// pq does not expose known nullability; native stored columns do,
		// including the same description when the result is empty.
		for _, query := range []string{"SELECT id,a,b FROM c ORDER BY id", "SELECT id,a,b FROM c WHERE id=999 ORDER BY id"} {
			rows, err := c.QueryContext(p.Ctx, query)
			if err != nil { t.Fatal(err) }
			columns, err := rows.ColumnTypes()
			if err != nil { rows.Close(); t.Fatal(err) }
			if len(columns) != 3 { rows.Close(); t.Fatal("metadata width", len(columns)) }
			for _, column := range columns {
				nullable, known := column.Nullable()
				if nullable != !b.PG || known != !b.PG { rows.Close(); t.Fatalf("%s nullable=%v/%v", column.Name(), nullable, known) }
			}
			if err := rows.Close(); err != nil { t.Fatal(err) }
		}
		run(fixture.Exec("CREATE TABLE null_first(a int)", 0))
		run(fixture.Exec("CREATE TRIGGER null_guard AFTER INSERT ON null_first FOR EACH ROW EXECUTE FUNCTION check_primary_key('a','missing','absent','x','y')", 0))
		run(fixture.Exec("INSERT INTO null_first VALUES(NULL)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO null_first VALUES(1)", refintError("42703", `there is no attribute "missing" in relation "null_first"`)))
		refintNotices(t, notices)
	})
}

func TestReferenceRefintRestrict(t *testing.T) {
	refintCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE p(id int)", 0)); run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE TABLE c(id int,pid int)", 0)); run(fixture.Exec("INSERT INTO c VALUES(10,1)", 1))
		run(fixture.Exec("CREATE TRIGGER guard AFTER DELETE OR UPDATE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','restrictive','id','c','pid')", 0))
		d := refintError("09000", `"guard": tuple is referenced in "c"`)
		run(fixture.ErrorDiagnostic("DELETE FROM p WHERE id=1", d))
		run(fixture.Prepared(fixture.ErrorDiagnostic("UPDATE p SET id=$1 WHERE id=$2", d, 3, 1)))
		run(fixture.Exec("UPDATE p SET id=id WHERE id=1", 1))
		run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)},{int64(2)}}))
		run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id","pid"}, []string{"INT4","INT4"}, [][]any{{int64(10),int64(1)}}))
		run(fixture.Exec("DELETE FROM p WHERE id=2", 1))
		refintNotices(t, notices)
	})
}

func TestReferenceRefintCascadeDelete(t *testing.T) {
	refintCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE TABLE p(id int)", "CREATE TABLE c1(id int,pid int)", "CREATE TABLE c2(id int,pid int)"} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec("INSERT INTO p VALUES(1),(2),(3)", 3))
		run(fixture.Exec("INSERT INTO c1 VALUES(10,1),(11,1),(12,2),(13,NULL)", 4))
		run(fixture.Exec("INSERT INTO c2 VALUES(20,1),(21,2)", 2))
		run(fixture.Exec("CREATE TRIGGER cascade_guard AFTER DELETE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('2','Cascade anything','id','c1','pid','c2','pid')", 0))
		run(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE id=$1", 1, 1)))
		refintNotices(t, notices, "cascade_guard: 2 tuple(s) of c1 are deleted", "cascade_guard: 1 tuple(s) of c2 are deleted")
		run(fixture.Select("SELECT id,pid FROM c1 ORDER BY id", []string{"id","pid"}, []string{"INT4","INT4"}, [][]any{{int64(12),int64(2)},{int64(13),nil}}))
		run(fixture.Select("SELECT id,pid FROM c2 ORDER BY id", []string{"id","pid"}, []string{"INT4","INT4"}, [][]any{{int64(21),int64(2)}}))
		run(fixture.Exec("DELETE FROM p WHERE id=3", 1))
		refintNotices(t, notices, "cascade_guard: 0 tuple(s) of c1 are deleted", "cascade_guard: 0 tuple(s) of c2 are deleted")
		run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
}

func refintCascadeUpdates(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	for _, q := range []string{"CREATE TABLE p(id int)","CREATE TABLE c(id int,pid int)"} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2)); run(fixture.Exec("INSERT INTO c VALUES(10,1),(20,2)", 2))
	run(fixture.Exec("CREATE TRIGGER update_guard AFTER UPDATE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','c','id','c','pid')", 0))
	q := "UPDATE p SET id=$1 WHERE id=$2"
	held, err := c.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }; defer held.Close()
	for _, pair := range [][2]int{{11,1},{22,2},{33,11}} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor:c, Statement:held, SQL:q}, r, fixture.Exec(q,1,pair[0],pair[1]))
		refintNotices(t, notices, "update_guard: 1 tuple(s) of c are updated")
	}
	run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id","pid"}, []string{"INT4","INT4"}, [][]any{{int64(10),int64(33)},{int64(20),int64(22)}}))
	run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(22)},{int64(33)}}))
}

func TestReferenceRefintCascadeUpdate(t *testing.T) {
	refintCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		refintCascadeUpdates(t,p,c,r,notices)
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE text_parent(k text)",0)); run(fixture.Exec("CREATE TABLE text_child(k text)",0))
		run(fixture.Exec("INSERT INTO text_parent VALUES('old')",1)); run(fixture.Exec("INSERT INTO text_child VALUES('old')",1))
		run(fixture.Exec("CREATE TRIGGER text_guard AFTER UPDATE ON text_parent FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','c','k','text_child','k')",0))
		value := "O'Reilly\\雪"
		run(fixture.Prepared(fixture.Exec("UPDATE text_parent SET k=$1",1,value)))
		refintNotices(t,notices,"text_guard: 1 tuple(s) of text_child are updated")
		run(fixture.Select("SELECT k FROM text_child",[]string{"k"},[]string{"TEXT"},[][]any{{value}}))
		run(fixture.Exec("UPDATE text_parent SET k=NULL",1))
		refintNotices(t,notices,"text_guard: 1 tuple(s) of text_child are updated")
		run(fixture.Select("SELECT k FROM text_child",[]string{"k"},[]string{"TEXT"},[][]any{{nil}}))
	})
}

func TestReferenceRefintSetNull(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE p(a int,b text)",0)); run(fixture.Exec("CREATE TABLE c(id int,a int,b text)",0))
		run(fixture.Exec("INSERT INTO p VALUES(1,'one'),(2,'two'),(NULL,'null')",3))
		run(fixture.Exec("INSERT INTO c VALUES(10,1,'one'),(20,2,'two'),(30,NULL,'one'),(40,1,NULL)",4))
		run(fixture.Exec("CREATE TRIGGER clear_guard AFTER DELETE OR UPDATE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','S','a','b','c','a','b')",0))
		run(fixture.Exec("UPDATE p SET a=a,b=b WHERE a=1",1)); refintNotices(t,notices)
		run(fixture.Exec("DELETE FROM p WHERE a=1",1)); refintNotices(t,notices,"clear_guard: 1 tuple(s) of c are set to null")
		run(fixture.Exec("UPDATE p SET a=22,b='changed' WHERE a=2",1)); refintNotices(t,notices,"clear_guard: 1 tuple(s) of c are set to null")
		run(fixture.Select("SELECT id,a,b FROM c ORDER BY id",[]string{"id","a","b"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(10),nil,nil},{int64(20),nil,nil},{int64(30),nil,"one"},{int64(40),int64(1),nil}}))
		run(fixture.Exec("DROP TRIGGER clear_guard ON p",0))
		run(fixture.Exec("CREATE TRIGGER skip_guard AFTER DELETE OR UPDATE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','c','a','missing','absent','x','y')",0))
		run(fixture.Exec("UPDATE p SET a=8 WHERE b='null'",1)); refintNotices(t,notices)
	})
}
