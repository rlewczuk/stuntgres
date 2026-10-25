package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsDiagnosticsNested(t *testing.T) {
	for _, kind := range []string{"not-null", "check", "foreign-key"} {
		for _, security := range []string{"force=false", "enabled-unforced", "force=true"} {
			force := security == "force=true"
			t.Run(kind+"/"+security, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"fa_actor", "fa_owner"})
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					for _, q := range []string{"CREATE ROLE fa_actor", "CREATE ROLE fa_owner", "GRANT USAGE ON SCHEMA public TO fa_actor", "GRANT USAGE,CREATE ON SCHEMA public TO fa_owner", "CREATE TABLE p(id INT PRIMARY KEY)", "GRANT DELETE ON p TO fa_actor", "GRANT REFERENCES ON p TO fa_owner"} {
						run(fixture.Exec(q, 0))
					}
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.Ctx, "RESET ROLE")
					step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					var login string
					if err := conn.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&login); err != nil {
						t.Fatal(err)
					}
					def, action := " NOT NULL", "SET NULL"
					d := fixture.ForeignKeyError("23502", `null value in column "pid" of relation "c" violates not-null constraint`, "Failing row contains (10, null, secret).", "c", "")
					d.Column = "pid"
					if kind == "check" {
						def, action = " CONSTRAINT ck CHECK(pid>1)", "SET DEFAULT"
						d = fixture.ForeignKeyError("23514", `new row for relation "c" violates check constraint "ck"`, "Failing row contains (10, 1, secret).", "c", "ck")
					}
					if kind == "foreign-key" {
						def, action = "", "SET DEFAULT"
						d = fixture.ForeignKeyChild("c", "fk", `Key (pid)=(1) is not present in table "p".`)
					}
					step(fixture.Exec("SET ROLE fa_owner", 0))
					step(fixture.Exec("CREATE TABLE c(id INT,pid INT DEFAULT 1"+def+" CONSTRAINT fk REFERENCES p ON DELETE "+action+",note TEXT)", 0))
					step(fixture.Exec("RESET ROLE", 0))
					if kind != "foreign-key" {
						run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					}
					run(fixture.Exec("INSERT INTO p VALUES(2)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(10,2,'secret')", 1))
					if security != "force=false" {
						run(fixture.Exec("ALTER TABLE c ENABLE ROW LEVEL SECURITY", 0))
					}
					if force {
						run(fixture.Exec("ALTER TABLE c FORCE ROW LEVEL SECURITY", 0))
					}
					// Pinned18.6 confirms SPI-local NN/CHECK retain child-owner
					// row detail and SPI Context even under FORCE. Child FK
					// after-events independently suppress key detail under FORCE.
					if kind == "foreign-key" {
						if force {
							d.Detail = `Key is not present in table "p".`
						}
					} else {
						d = foreignActionsContext(d, foreignActionsSQL("c", "pid", action, "DELETE"))
					}
					step(fixture.Exec("SET ROLE fa_actor", 0))
					for _, prepared := range []bool{false, true} {
						step(fixture.ForeignKeyFail("DELETE FROM p", d, prepared))
						step(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"fa_actor", login}}))
						run(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(10), int64(2), "secret"}}))
						if kind != "foreign-key" {
							run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
						} else {
							run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 2))
						}
					}
					step(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"fa_actor"}}))
					step(fixture.Exec("RESET ROLE", 0))
					run(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(10), int64(2), "secret"}}))
					run(foreignKeyIntRows("SELECT id FROM p WHERE id=2", "id", 2))
				})
			})
		}
	}
}
