package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexDistinctOwnerControls(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"index_schema_owner", "index_table_owner"})
			fixture.SchemaReferenceCleanup(t, "s")
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					backend.DB.SetMaxOpenConns(1)
					var actor *sql.Conn
					var executor fixture.Executor = backend.DB
					reset := backend.DB.ExecContext
					if !nonContext {
						var err error
						actor, err = backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer actor.Close()
						executor = actor
						reset = actor.ExecContext
					}
					run := func(s fixture.Step) {
						s.NonContext = nonContext
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
					}
					run(fixture.Exec("CREATE USER index_schema_owner", 0))
					run(fixture.Exec("CREATE USER index_table_owner", 0))
					const seed = `CREATE SCHEMA s AUTHORIZATION index_schema_owner; SET SESSION AUTHORIZATION index_table_owner; CREATE TABLE s.t(n INT,s TEXT); INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b'); CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST);`
					var dump bytes.Buffer
					if !backend.PG {
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(seed)); err != nil {
							t.Fatal("trusted split-owner replay", err)
						}
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						for _, want := range []string{`CREATE SCHEMA "s" AUTHORIZATION "index_schema_owner";`, `SET SESSION AUTHORIZATION 'index_table_owner';`, `CREATE INDEX "idx" ON "s"."t" USING btree ("n" DESC NULLS LAST, "s" ASC NULLS FIRST);`} {
							if !strings.Contains(dump.String(), want) {
								t.Fatal(want, dump.String())
							}
						}
					}
					for round := range 2 {
						if backend.PG {
							if round != 0 {
								run(fixture.Exec("DELETE FROM s.t WHERE n=3", 1))
							}
							run(fixture.Exec("DROP SCHEMA IF EXISTS s CASCADE", 0))
							run(fixture.Exec("CREATE SCHEMA s AUTHORIZATION index_schema_owner", 0))
							if round == 0 {
								// Incoming PG witness creates as bootstrap, then transfers ownership.
								run(fixture.Exec("CREATE TABLE s.t(n INT,s TEXT)", 0))
								run(fixture.Exec("INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3))
								run(fixture.Exec("CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
								run(fixture.Exec("ALTER TABLE s.t OWNER TO index_table_owner", 0))
								run(fixture.Exec("GRANT USAGE ON SCHEMA s TO index_table_owner", 0))
							} else {
								// Original PG counterpart creates under the table owner's authority.
								run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA s TO index_table_owner", 0))
								run(fixture.Exec("SET SESSION AUTHORIZATION index_table_owner", 0))
								defer reset(p.Ctx, "RESET SESSION AUTHORIZATION")
								run(fixture.Exec("CREATE TABLE s.t(n INT,s TEXT)", 0))
								run(fixture.Exec("INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3))
								run(fixture.Exec("CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
								run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
								run(fixture.Exec("REVOKE ALL ON SCHEMA s FROM index_table_owner", 0))
							}
						} else {
							if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
								t.Fatal(err)
							}
							if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
								t.Fatal(err)
							}
						}
						login := "postgres"
						if backend.PG {
							login = "stuntgres"
						}
						run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{login}}))
						run(fixture.Select("SELECT n,s FROM s.t ORDER BY n DESC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}, {nil, nil}}))
						run(fixture.Exec("SET SESSION AUTHORIZATION index_table_owner", 0))
						defer reset(p.Ctx, "RESET SESSION AUTHORIZATION")
						run(fixture.ImplicitError("CREATE INDEX denied ON s.t(n)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema s"}))
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Exec("INSERT INTO s.t VALUES(3,'c')", 1))
						run(fixture.Select("SELECT n FROM s.t ORDER BY n DESC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}))
					}
					run(fixture.Exec("DROP SCHEMA s CASCADE", 0))
					run(fixture.Exec("DROP ROLE index_table_owner", 0))
					run(fixture.Exec("DROP ROLE index_schema_owner", 0))
				})
			}
		})
	}
}
