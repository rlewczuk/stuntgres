package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsAuthorization(t *testing.T) {
	for _, action := range []string{"CASCADE", "SET NULL", "SET DEFAULT"} {
		for _, direction := range []string{"DELETE", "UPDATE"} {
			t.Run(direction+"/"+action, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"fa_builder", "fa_actor"})
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					for _, q := range []string{"CREATE ROLE fa_builder", "CREATE ROLE fa_actor", "CREATE TABLE p(id INT PRIMARY KEY)", "GRANT REFERENCES(id) ON p TO fa_builder", "GRANT DELETE,UPDATE,SELECT ON p TO fa_actor"} {
						run(fixture.Exec(q, 0))
					}
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.Ctx, "RESET ROLE")
					step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					step(fixture.Exec("SET ROLE fa_builder", 0))
					fixture.WithPublicSchemaOwner(t, p, b, "fa_builder", func() {
						step(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON "+direction+" "+action+",note TEXT)", 0))
					})
					step(fixture.Exec("RESET ROLE", 0))
					run(fixture.Exec("REVOKE REFERENCES ON p FROM fa_builder", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
					run(fixture.Exec("INSERT INTO c VALUES(10,2,'hidden'),(11,2,'second')", 2))
					run(fixture.Exec("ALTER TABLE c ENABLE ROW LEVEL SECURITY", 0))
					run(fixture.Exec("ALTER TABLE c FORCE ROW LEVEL SECURITY", 0))
					step(fixture.Exec("SET ROLE fa_actor", 0))
					step(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"fa_actor"}}))
					q := "DELETE FROM p WHERE id=2"
					if direction == "UPDATE" {
						q = "UPDATE p SET id=3 WHERE id=2"
					}
					step(fixture.Prepared(fixture.Exec(q, 1)))
					step(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"fa_actor"}}))
					step(fixture.Exec("RESET ROLE", 0))
					var rows [][]any
					var pid any = nil
					if action == "SET DEFAULT" {
						pid = int64(1)
					}
					if action == "CASCADE" && direction == "UPDATE" {
						pid = int64(3)
					}
					if action != "CASCADE" || direction != "DELETE" {
						rows = [][]any{{int64(10), pid, "hidden"}, {int64(11), pid, "second"}}
					}
					run(fixture.Select("SELECT id,pid,note FROM c ORDER BY id", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, rows))
				})
			})
		}
	}
}

func TestReferenceForeignActionsAuthorizationOwnerDefaults(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_builder", "fa_actor"})
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE ROLE fa_builder", "CREATE ROLE fa_actor", "CREATE TABLE p(id TEXT PRIMARY KEY)", "GRANT REFERENCES ON p TO fa_builder", "GRANT DELETE,SELECT ON p TO fa_actor"} {
			run(fixture.Exec(q, 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		step(fixture.Exec("SET ROLE fa_builder", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "fa_builder", func() {
			step(fixture.Exec("CREATE TABLE c(pid TEXT DEFAULT CURRENT_USER CONSTRAINT fk REFERENCES p ON DELETE SET DEFAULT)", 0))
		})
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("INSERT INTO p VALUES('old'),('fa_builder'),('fa_actor')", 3))
		run(fixture.Exec("INSERT INTO c VALUES('old')", 1))
		stmt, err := conn.PrepareContext(p.Ctx, "DELETE FROM p WHERE id=$1")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		step(fixture.Exec("SET ROLE fa_actor", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: "DELETE FROM p WHERE id=$1"}, b.Recorder, fixture.Exec("DELETE FROM p WHERE id=$1", 1, "old"))
		step(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"fa_actor"}}))
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"TEXT"}, [][]any{{"fa_builder"}}))
	})
}

func TestReferenceForeignActionsAuthorizationProvider(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_builder", "fa_actor"})
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE ROLE fa_builder", "CREATE ROLE fa_actor", "CREATE SEQUENCE action_provider START 10", "ALTER SEQUENCE action_provider OWNER TO fa_builder", "CREATE TABLE p(id BIGINT PRIMARY KEY)", "GRANT REFERENCES ON p TO fa_builder", "GRANT DELETE,SELECT ON p TO fa_actor"} {
			run(fixture.Exec(q, 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		step(fixture.Exec("SET ROLE fa_builder", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "fa_builder", func() {
			step(fixture.Exec("CREATE TABLE c(pid BIGINT DEFAULT nextval('action_provider') CONSTRAINT fk REFERENCES p ON DELETE SET DEFAULT)", 0))
		})
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("INSERT INTO p VALUES(2),(10),(11)", 3))
		run(fixture.Exec("INSERT INTO c VALUES(2)", 1))
		step(fixture.Exec("SET ROLE fa_actor", 0))
		step(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE id=2", 1)))
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT8"}, [][]any{{int64(10)}}))
		// Shared sequence ownership is supported; sequence GRANT is an adjacent
		// native capability boundary. Transfer ownership instead of granting a shim.
		run(fixture.Exec("ALTER SEQUENCE action_provider OWNER TO postgres", 0))
		step(fixture.Exec("SET ROLE fa_actor", 0))
		d := foreignActionsContext(foreignKeyError("42501", "permission denied for sequence action_provider", "", "", ""), foreignActionsSQL("c", "pid", "SET DEFAULT", "DELETE"))
		for _, prepared := range []bool{false, true} {
			step(foreignKeyFail("DELETE FROM p WHERE id=10", d, prepared))
		}
		step(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"fa_actor"}}))
		step(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(11)}}))
		run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT8"}, [][]any{{int64(10)}}))
	})
}

func TestReferenceForeignActionsAuthorizationParentRLS(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_actor"})
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE ROLE fa_actor", "CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE)", "GRANT SELECT,DELETE ON p TO fa_actor", "ALTER TABLE p ENABLE ROW LEVEL SECURITY"} {
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
		step(fixture.Prepared(fixture.Exec("DELETE FROM p", 0)))
		step(fixture.Exec("RESET ROLE", 0))
		run(foreignKeyIntRows("SELECT id FROM p", "id", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
	})
}

func TestReferenceForeignActionsDiagnostics(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT"} {
		for _, access := range []string{"none", "partial", "columns", "table"} {
			t.Run(fmt.Sprintf("%s/%s", action, access), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"fa_actor"})
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					for _, q := range []string{"CREATE ROLE fa_actor", "CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", "CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON DELETE " + action + " ON UPDATE " + action + ")", "GRANT DELETE,UPDATE ON p TO fa_actor"} {
						run(fixture.Exec(q, 0))
					}
					if b.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fa_actor", 0))
					}
					if access == "partial" {
						run(fixture.Exec("GRANT SELECT(a) ON p TO fa_actor", 0))
					}
					if access == "columns" {
						run(fixture.Exec("GRANT SELECT(a,b) ON p TO fa_actor", 0))
					}
					if access == "table" {
						run(fixture.Exec("GRANT SELECT ON p TO fa_actor", 0))
					}
					run(fixture.Exec("INSERT INTO p VALUES(7,2)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(7,2)", 1))
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.Ctx, "RESET ROLE")
					step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					step(fixture.Exec("SET ROLE fa_actor", 0))
					detail := `Key is still referenced from table "c".`
					if access == "columns" || access == "table" {
						detail = `Key (a, b)=(7, 2) is still referenced from table "c".`
					}
					d := foreignKeyParent("p", "c", "fk", detail)
					if action == "RESTRICT" {
						detail = `Key is referenced from table "c".`
						if access == "columns" || access == "table" {
							detail = `Key (a, b)=(7, 2) is referenced from table "c".`
						}
						d = foreignActionsRestrict("p", "c", "fk", detail)
					}
					for _, prepared := range []bool{false, true} {
						step(foreignKeyFail("DELETE FROM p", d, prepared))
						step(foreignKeyFail("UPDATE p SET a=8", d, prepared))
					}
					step(fixture.Exec("RESET ROLE", 0))
					run(fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(2)}}))
				})
			})
		}
	}
}
