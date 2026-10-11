package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceForeignKeyDropColumnCaller(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
			run(fixture.Exec("CREATE TABLE p(a INT,b INT UNIQUE)", 0))
			run(fixture.Exec("CREATE TABLE child(x INT CONSTRAINT child_b_fk REFERENCES p(b))", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1,9)", 1))
			run(fixture.Exec("INSERT INTO child VALUES(9)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("ALTER TABLE p DROP b RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column b of table p because other objects depend on it", Detail: "constraint child_b_fk on table child depends on column b of table p", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT * FROM p", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
			}
			notices = nil
			run(fixture.Exec("ALTER TABLE p DROP b CASCADE", 0))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to constraint child_b_fk on table child"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			run(fixture.Select("SELECT * FROM child", []string{"x"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Select("SELECT f.conname FROM pg_constraint f JOIN pg_class r ON f.conrelid=r.oid WHERE r.relname='child'", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO child VALUES(99)", 1))
			run(fixture.Exec("CREATE TABLE mixed_parent(k INT UNIQUE)", 0))
			run(fixture.Exec("CREATE TABLE mixed_child(k INT CONSTRAINT mixed_fk REFERENCES mixed_parent(k))", 0))
			run(fixture.Exec("CREATE VIEW mixed_view AS SELECT k FROM mixed_parent", 0))
			run(fixture.Exec("INSERT INTO mixed_parent VALUES(7)", 1))
			run(fixture.Exec("INSERT INTO mixed_child VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("ALTER TABLE mixed_parent DROP k RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column k of table mixed_parent because other objects depend on it", Detail: "constraint mixed_fk on table mixed_child depends on column k of table mixed_parent\nview mixed_view depends on column k of table mixed_parent", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT k FROM mixed_view", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			notices = nil
			run(fixture.Exec("ALTER TABLE mixed_parent DROP k CASCADE", 0))
			want = []fixture.Diagnostic{
				{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to constraint mixed_fk on table mixed_child\ndrop cascades to view mixed_view"},
			}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			run(fixture.Select("SELECT k FROM mixed_child", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("INSERT INTO mixed_child VALUES(99)", 1))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='mixed_view'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TABLE self_fk(id INT PRIMARY KEY,kept INT CHECK(kept>0),CONSTRAINT self_ref FOREIGN KEY(id) REFERENCES self_fk)", 0))
			run(fixture.Exec("INSERT INTO self_fk VALUES(1,1)", 1))
			notices = nil
			run(fixture.Exec("ALTER TABLE self_fk DROP id RESTRICT", 0))
			if len(notices) != 0 {
				t.Fatal("local AUTO FK emitted cascade notice", notices)
			}
			run(fixture.Select("SELECT kept FROM self_fk", []string{"kept"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT f.conname FROM pg_constraint f JOIN pg_class r ON f.conrelid=r.oid WHERE r.relname='self_fk' AND f.contype='f'", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO self_fk VALUES(2)", 1))
		})
	}
}

func TestReferenceForeignKeySchemaCaller(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "fk_s1", "fk_s2")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA fk_s1", "CREATE SCHEMA fk_s2", "CREATE TABLE fk_s1.p(id INT PRIMARY KEY)", "CREATE TABLE fk_s1.c(pid INT CONSTRAINT fk REFERENCES fk_s1.p)", "INSERT INTO fk_s1.p VALUES(7)", "INSERT INTO fk_s1.c VALUES(7)"} {
				n := int64(0)
				if len(q) > 6 && q[:6] == "INSERT" {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			var old int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='fk'").Scan(&old); err != nil {
				t.Fatal(err)
			}
			run(fixture.Prepared(fixture.Exec("ALTER TABLE fk_s1.p SET SCHEMA fk_s2", 0)))
			var moved int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='fk'").Scan(&moved); err != nil || moved != old {
				t.Fatal(old, moved, err)
			}
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (pid) REFERENCES fk_s2.p(id)"}}))
			run(fixture.Exec("INSERT INTO fk_s1.c VALUES(7)", 1))
			s := fixture.ImplicitError("INSERT INTO fk_s1.c VALUES(9)", fixture.Diagnostic{Code: "23503", Message: `insert or update on table "c" violates foreign key constraint "fk"`, Detail: `Key (pid)=(9) is not present in table "p".`, Schema: "fk_s1", Table: "c", Constraint: "fk"})
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Exec("DROP SCHEMA fk_s2 CASCADE", 0))
			run(fixture.Select("SELECT pid FROM fk_s1.c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
			run(fixture.Exec("INSERT INTO fk_s1.c VALUES(9)", 1))
		})
	}
}
