package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceForeignKeyAtomicity(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", 0))
		run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT CONSTRAINT fk REFERENCES p)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1,'x'),(2,'y')", 2))
		run(fixture.Exec("INSERT INTO c VALUES(10,1),(20,2)", 2))
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail("INSERT INTO c VALUES(30,1),(40,9)", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			run(foreignKeyFail("UPDATE c SET pid=CASE WHEN id=10 THEN 2 ELSE 9 END", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			run(foreignKeyFail("DELETE FROM p", foreignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`), prepared))
			run(foreignKeyFail("UPDATE p SET id=id+10", foreignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`), prepared))
			run(fixture.Select("SELECT id,note FROM p ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}}))
			run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(1)}, {int64(20), int64(2)}}))
		}
		run(fixture.Exec("UPDATE p SET id=id,note='changed'", 2))
		run(fixture.Exec("CREATE TABLE self(id INT PRIMARY KEY,pid INT REFERENCES self)", 0))
		run(fixture.Exec("INSERT INTO self VALUES(2,1),(1,NULL)", 2))
		run(fixture.Exec("DELETE FROM self", 2))
	})
}

func TestReferenceForeignKeyTransactions(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		for _, prepared := range []bool{false, true} {
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				step(fixture.Exec("SAVEPOINT keep", 0))
				step(foreignKeyFail("INSERT INTO c VALUES(9)", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
				step(foreignKeyFail("SELECT pid FROM c", fixture.TransactionAborted(), false))
				step(fixture.Exec("ROLLBACK TO keep", 0))
				step(fixture.Exec("DELETE FROM c", 1))
				step(fixture.Exec("DELETE FROM p WHERE id=1", 1))
				step(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 2))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}()
			run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
			run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
		}
	})
}

func TestReferenceForeignKeyProducers(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT CONSTRAINT fk REFERENCES p)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO c SELECT id,id FROM p", 2))
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail("INSERT INTO c SELECT 3,9 FROM p WHERE id=1", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			run(foreignKeyFail("INSERT INTO c VALUES(3,9) ON CONFLICT DO NOTHING", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			skip := fixture.Exec("INSERT INTO c VALUES(1,9) ON CONFLICT DO NOTHING", 0)
			skip.Prepared = prepared
			run(skip)
			// The incoming conflict UPDATE owner is admitted; its real final
			// image must fail FK validation rather than an obsolete suffix gate.
			run(foreignKeyFail("INSERT INTO c VALUES(1,9) ON CONFLICT(id) DO UPDATE SET pid=excluded.pid", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
			run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
			for _, q := range []string{"INSERT INTO c VALUES(1,9) ON CONFLICT(id) DO UPDATE SET pid=excluded.pid RETURNING pid", "INSERT INTO c SELECT 3,9 FROM p WHERE id=1 RETURNING pid"} {
				s := foreignKeyFail(q, foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared)
				s.SelectRows = true
				run(s)
			}
			update := foreignKeyFail("UPDATE c SET pid=9 FROM p WHERE c.id=p.id RETURNING c.pid", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared)
			update.SelectRows = true
			run(update)
			s := foreignKeyFail("DELETE FROM p USING c WHERE p.id=c.pid RETURNING p.id", foreignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`), prepared)
			s.SelectRows = true
			run(s)
			q := "MERGE INTO c USING (SELECT 1 AS id,9 AS pid) s ON c.id=s.id WHEN MATCHED THEN UPDATE SET pid=s.pid"
			run(foreignKeyFail(q, foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			run(foreignKeyFail("MERGE INTO c USING (SELECT 3 AS id,9 AS pid) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.pid)", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), prepared))
			run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
			run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
		}
		run(fixture.Exec("CREATE TABLE default_child(pid INT DEFAULT 9 CONSTRAINT default_fk REFERENCES p)", 0))
		run(foreignKeyFail("INSERT INTO default_child DEFAULT VALUES", foreignKeyChild("default_child", "default_fk", `Key (pid)=(9) is not present in table "p".`), false))
		run(foreignKeyIntRows("SELECT pid FROM default_child", "pid"))
	})
}

func TestReferenceForeignKeyAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fk_builder", "fk_writer"})
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE ROLE fk_builder", "CREATE ROLE fk_writer"} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", "GRANT REFERENCES(a) ON p TO fk_builder"} {
			run(fixture.Exec(q, 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error("authorization fixture reset", err)
			}
		}()
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		step(fixture.Exec("SET ROLE fk_builder", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "fk_builder", func() {
			step(foreignKeyFail("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p)", foreignKeyError("42501", "permission denied for table p", "", "", ""), false))
		})
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT REFERENCES(b) ON p TO fk_builder", 0))
		step(fixture.Exec("SET ROLE fk_builder", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "fk_builder", func() {
			step(fixture.Exec("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p)", 0))
		})
		step(fixture.Exec("RESET ROLE", 0))
		for _, q := range []string{"REVOKE REFERENCES ON p FROM fk_builder", "GRANT INSERT ON c TO fk_writer", "GRANT DELETE ON p TO fk_writer"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1,2)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1,2)", 1))
		step(fixture.Exec("SET ROLE fk_writer", 0))
		for _, prepared := range []bool{false, true} {
			step(foreignKeyFail("INSERT INTO c VALUES(9,9)", foreignKeyChild("c", "fk", `Key is not present in table "p".`), prepared))
			step(foreignKeyFail("DELETE FROM p", foreignKeyParent("p", "c", "fk", `Key is still referenced from table "c".`), prepared))
		}
		step(fixture.Exec("INSERT INTO c VALUES(1,2)", 1))
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}}))
	})
}
