package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsPrepared(t *testing.T) {
	for _, sqlPrepare := range []bool{false, true} {
		for _, direction := range []string{"DELETE", "UPDATE"} {
			for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
				t.Run(fmt.Sprintf("sql=%t/%s/%s", sqlPrepare, direction, action), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						run(fixture.Exec("CREATE TABLE c(pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON "+direction+" "+action+")", 0))
						run(fixture.Exec("INSERT INTO p VALUES(0),(1),(2)", 3))
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
						q := "DELETE FROM p WHERE id=$1"
						if direction == "UPDATE" {
							q = "UPDATE p SET id=3 WHERE id=$1"
						}
						execute := step
						executeQ := "EXECUTE action_parent(2)"
						if sqlPrepare {
							step(fixture.Exec("PREPARE action_parent(int) AS "+q, 0))
							defer conn.ExecContext(p.Ctx, "DEALLOCATE action_parent")
						} else {
							stmt, err := conn.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executeQ = q
							execute = func(s fixture.Step) {
								s.Args = []any{2}
								fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
							}
						}
						// The child and changed default arrive after Prepare, so a
						// cached executable/default/row image cannot satisfy this test.
						step(fixture.Exec("INSERT INTO c VALUES(2),(2)", 2))
						step(fixture.Exec("ALTER TABLE c ALTER pid SET DEFAULT 0", 0))
						if action == "NO ACTION" || action == "RESTRICT" {
							d := foreignKeyParent("p", "c", "fk", `Key (id)=(2) is still referenced from table "c".`)
							if action == "RESTRICT" {
								d = foreignActionsRestrict("p", "c", "fk", `Key (id)=(2) is referenced from table "c".`)
							}
							execute(foreignKeyFail(executeQ, d, false))
							step(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 0, 1, 2))
							step(foreignKeyIntRows("SELECT pid FROM c", "pid", 2, 2))
						} else {
							execute(fixture.Exec(executeQ, 1))
							var rows [][]any
							var value any = nil
							if action == "SET DEFAULT" {
								value = int64(0)
							}
							if action == "CASCADE" && direction == "UPDATE" {
								value = int64(3)
							}
							if action != "CASCADE" || direction != "DELETE" {
								rows = [][]any{{value}, {value}}
							}
							step(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, rows))
						}
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsPreparedLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "fa_prepared_moved")
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("CREATE TABLE c(a INT,b INT DEFAULT 1,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON DELETE SET DEFAULT (b))", 0))
		run(fixture.Exec("INSERT INTO p VALUES(7,1),(7,2),(7,3)", 3))
		run(fixture.Exec("INSERT INTO c VALUES(7,2)", 1))
		q := "DELETE FROM p WHERE b=$1 RETURNING a,b"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, ddl := range []string{"ALTER TABLE c RENAME b TO author", "ALTER TABLE c RENAME TO renamed", "CREATE SCHEMA fa_prepared_moved", "ALTER TABLE renamed SET SCHEMA fa_prepared_moved", "ALTER TABLE fa_prepared_moved.renamed ALTER author TYPE BIGINT", "ALTER TABLE fa_prepared_moved.renamed ALTER author SET DEFAULT 3"} {
			run(fixture.Exec(ddl, 0))
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(2)}}, 2))
		run(fixture.Select("SELECT a,author FROM fa_prepared_moved.renamed", []string{"a", "author"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(3)}}))
		run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (a, author) REFERENCES p(a, b) ON DELETE SET DEFAULT (author)"}}))
		run(fixture.Exec("ALTER TABLE fa_prepared_moved.renamed DROP CONSTRAINT fk", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(3)}}, 3))
		run(fixture.Select("SELECT a,author FROM fa_prepared_moved.renamed", []string{"a", "author"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(3)}}))
	})
}

func TestReferenceForeignActionsPreparedAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_actor"})
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE ROLE fa_actor", "CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE)", "GRANT SELECT,DELETE ON p TO fa_actor"} {
			run(fixture.Exec(q, 0))
		}
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fa_actor", 0))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		step(fixture.Exec("SET ROLE fa_actor", 0))
		q := "DELETE FROM p WHERE id=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("REVOKE DELETE ON p FROM fa_actor", 0))
		s := foreignKeyFail(q, foreignKeyError("42501", "permission denied for table p", "", "", ""), false)
		s.Args = []any{1}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
		run(fixture.Exec("GRANT DELETE ON p TO fa_actor", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 1))
		step(fixture.Exec("RESET ROLE", 0))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
	})
}
