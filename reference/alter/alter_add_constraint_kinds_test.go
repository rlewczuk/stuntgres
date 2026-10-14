package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintKinds(t *testing.T) {
	for _, kind := range addConstraintCases() {
		t.Run(kind.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,NULL)", 2))
				held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
				run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD "+kind.clause, 0)))
				run(fixture.Select("SELECT contype FROM pg_constraint WHERE conrelid='t'::regclass::oid AND contype=$1", []string{"contype"}, []string{"CHAR"}, [][]any{{kind.kind}}, kind.kind))
				write := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				write(fixture.Exec(held.SQL, 1, 3, "three"))
				write(fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad"))
				write(fixture.Exec(held.SQL, 1, 4, "four"))
				run(addConstraintRows([]any{int64(1), "one"}, []any{int64(2), nil}, []any{int64(3), "three"}, []any{int64(4), "four"}))
				for _, prepared := range []bool{false, true} {
					fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT a,note FROM t ORDER BY a", []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{kind.kind != "n" && kind.kind != "p", true}, 4)
				}
			})
		})
	}
	t.Run("generated_names_and_null_semantics", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'x'),(NULL,'y'),(NULL,'z')", 3))
			run(fixture.Exec("ALTER TABLE t ADD CHECK(a>0), ADD UNIQUE(a)", 0))
			run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"t_a_check", "c"}, {"t_a_key", "u"}}))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'another')", 1))
			run(fixture.Exec("CREATE TABLE nd(a INT,note TEXT)", 0))
			run(fixture.Exec("INSERT INTO nd VALUES(NULL,'old')", 1))
			run(fixture.Exec("ALTER TABLE nd ADD CONSTRAINT nd_key UNIQUE NULLS NOT DISTINCT(a)", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO nd VALUES(NULL,'new')", fixture.UniqueReferenceDuplicate("nd", "nd_key", "Key (a)=(null) already exists.")))
			run(fixture.Exec("CREATE TABLE composite(a INT,b TEXT,old INT UNIQUE)", 0))
			run(fixture.Exec("INSERT INTO composite VALUES(1,'x',7),(1,'y',8)", 2))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE composite ADD CONSTRAINT pair UNIQUE(a,b)", 0)))
			run(fixture.ErrorDiagnostic("INSERT INTO composite VALUES(2,'z',7)", fixture.UniqueReferenceDuplicate("composite", "composite_old_key", "Key (old)=(7) already exists.")))
			run(fixture.ErrorDiagnostic("INSERT INTO composite VALUES(1,'x',9)", fixture.UniqueReferenceDuplicate("composite", "pair", "Key (a, b)=(1, x) already exists.")))
			run(fixture.Exec("CREATE TABLE composite_pk(a INT,b TEXT)", 0))
			run(fixture.Exec("INSERT INTO composite_pk VALUES(1,'x'),(1,'y')", 2))
			run(fixture.Exec("ALTER TABLE composite_pk ADD PRIMARY KEY(a,b)", 0))
			run(fixture.Exec("CREATE TABLE named_nn(a INT)", 0))
			run(fixture.Exec("ALTER TABLE named_nn ADD NOT NULL a", 0))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='named_nn'::regclass::oid", []string{"conname"}, []string{"NAME"}, [][]any{{"named_nn_a_not_null"}}))
		})
	})
	t.Run("foreign_key_shapes", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE parent(a INT,b BIGINT,PRIMARY KEY(a,b),UNIQUE(b,a))", 0))
			run(fixture.Exec("INSERT INTO parent VALUES(1,2),(3,4)", 2))
			run(fixture.Exec("CREATE TABLE child(x INT,y BIGINT)", 0))
			run(fixture.Exec("INSERT INTO child VALUES(1,2),(NULL,9),(3,4)", 3))
			run(fixture.Exec("ALTER TABLE child ADD CONSTRAINT implicit FOREIGN KEY(x,y) REFERENCES parent", 0))
			run(fixture.Exec("ALTER TABLE child ADD CONSTRAINT reordered FOREIGN KEY(y,x) REFERENCES parent(b,a)", 0))
			run(fixture.Select("SELECT conname,conkey::text,confkey::text,confrelid='parent'::regclass::oid,convalidated FROM pg_constraint WHERE conrelid='child'::regclass::oid ORDER BY conname", []string{"conname", "conkey", "confkey", "?column?", "convalidated"}, []string{"NAME", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"implicit", "{1,2}", "{1,2}", true, true}, {"reordered", "{2,1}", "{2,1}", true, true}}))
			run(fixture.Exec("CREATE TABLE full_child(x BIGINT,y INT)", 0))
			run(fixture.Exec("INSERT INTO full_child VALUES(1,2),(NULL,NULL)", 2))
			run(fixture.Exec("ALTER TABLE full_child ADD CONSTRAINT full_fk FOREIGN KEY(x,y) REFERENCES parent(a,b) MATCH FULL", 0))
			d := addConstraintFK("full_child", "full_fk", "x, y", "1, null", "parent")
			d.Detail = "MATCH FULL does not allow mixing of null and nonnull key values."
			run(fixture.ErrorDiagnostic("INSERT INTO full_child VALUES(1,NULL)", d))
			run(fixture.Exec("CREATE TABLE self(id INT PRIMARY KEY,pid INT)", 0))
			run(fixture.Exec("INSERT INTO self VALUES(2,1),(1,NULL)", 2))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE self ADD FOREIGN KEY(pid) REFERENCES self", 0)))
			run(fixture.Exec("DELETE FROM self", 2))
		})
	})
	t.Run("exclusion_providers", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, method := range []string{"btree", "hash"} {
				run(fixture.Exec("CREATE TABLE "+method+"_t(a INT,active BOOL)", 0))
				run(fixture.Exec("INSERT INTO "+method+"_t VALUES(1,true),(1,false),(NULL,true)", 3))
				run(fixture.Exec("ALTER TABLE "+method+"_t ADD CONSTRAINT "+method+"_ex EXCLUDE USING "+method+"((a+1) WITH =) WHERE(active)", 0))
				run(fixture.Exec("INSERT INTO "+method+"_t VALUES(1,false),(NULL,true)", 2))
				run(fixture.ErrorDiagnostic("INSERT INTO "+method+"_t VALUES(1,true)", fixture.ExclusionReferenceConflict(method+"_t", method+"_ex", "(a + 1)", "2", "2")))
			}
			run(fixture.Exec("CREATE TABLE deferred(a INT)", 0))
			run(fixture.Exec("ALTER TABLE deferred ADD CONSTRAINT deferred_ex EXCLUDE(a WITH =) DEFERRABLE INITIALLY DEFERRED", 0))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO deferred VALUES(1),(1)", 2))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DELETE FROM deferred", 2))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	})
}
