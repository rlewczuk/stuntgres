package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsValuesCompositeActions(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", direction, action, prepared), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
						run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,b TEXT DEFAULT 'safe',a INT DEFAULT 1,note TEXT,CONSTRAINT fk FOREIGN KEY(b,a) REFERENCES p(b,a) ON "+direction+" "+action+")", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1,'safe'),(2,'old'),(8,'old')", 3))
						run(fixture.Exec("INSERT INTO c VALUES(10,'old',2,'first'),(11,'old',2,'second'),(12,'safe',1,'unaffected'),(13,NULL,2,'partial'),(14,'old',NULL,'partial'),(15,'old',8,'tenant')", 6))
						q := "DELETE FROM p WHERE a=2"
						if direction == "UPDATE" {
							q = "UPDATE p SET a=3,b='next' WHERE a=2"
						}
						rows := [][]any{{int64(10), "old", int64(2), "first"}, {int64(11), "old", int64(2), "second"}, {int64(12), "safe", int64(1), "unaffected"}, {int64(13), nil, int64(2), "partial"}, {int64(14), "old", nil, "partial"}, {int64(15), "old", int64(8), "tenant"}}
						if action == "NO ACTION" || action == "RESTRICT" {
							d := fixture.ForeignKeyParent("p", "c", "fk", `Key (b, a)=(old, 2) is still referenced from table "c".`)
							if action == "RESTRICT" {
								d = foreignActionsRestrict("p", "c", "fk", `Key (b, a)=(old, 2) is referenced from table "c".`)
							}
							run(fixture.ForeignKeyFail(q, d, prepared))
						} else {
							run(foreignActionsExec(q, 1, prepared))
							if action == "CASCADE" && direction == "DELETE" {
								rows = rows[2:]
							} else {
								var a, b any = nil, nil
								if action == "CASCADE" {
									a, b = int64(3), "next"
								}
								if action == "SET DEFAULT" {
									a, b = int64(1), "safe"
								}
								for i := range 2 {
									rows[i][1], rows[i][2] = b, a
								}
							}
						}
						run(fixture.Select("SELECT id,b,a,note FROM c ORDER BY id", []string{"id", "b", "a", "note"}, []string{"INT4", "TEXT", "INT4", "TEXT"}, rows))
						parents := [][]any{{int64(1), "safe"}, {int64(8), "old"}}
						if action == "NO ACTION" || action == "RESTRICT" {
							parents = [][]any{{int64(1), "safe"}, {int64(2), "old"}, {int64(8), "old"}}
						} else if direction == "UPDATE" {
							parents = [][]any{{int64(1), "safe"}, {int64(3), "next"}, {int64(8), "old"}}
						}
						run(fixture.Select("SELECT a,b FROM p ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, parents))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsConstraintsDeferredExclusion(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(id INT,pid INT DEFAULT 1 REFERENCES p ON DELETE SET DEFAULT,CONSTRAINT ex EXCLUDE USING btree(pid WITH =) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO c VALUES(10,1),(20,2)", 2))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		step(fixture.Exec("SAVEPOINT keep", 0))
		step(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE id=2", 1)))
		step(foreignKeyIntRows("SELECT pid FROM c ORDER BY id", "pid", 1, 1))
		d := fixture.ForeignKeyError("23P01", `conflicting key value violates exclusion constraint "ex"`, "Key (pid)=(1) conflicts with existing key (pid)=(1).", "c", "ex")
		step(fixture.ForeignKeyFail("SET CONSTRAINTS ex IMMEDIATE", d, false))
		step(fixture.ForeignKeyFail("SELECT 1", fixture.TransactionAborted(), false))
		step(fixture.Exec("ROLLBACK TO keep", 0))
		step(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
		step(foreignKeyIntRows("SELECT pid FROM c ORDER BY id", "pid", 1, 2))
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
		run(foreignKeyIntRows("SELECT pid FROM c ORDER BY id", "pid", 1, 2))
	})
}
