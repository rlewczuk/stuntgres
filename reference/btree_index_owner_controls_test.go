package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBtreeIndexDistinctOwnerControls(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			roleReferenceCleanup(t, []string{"index_schema_owner", "index_table_owner"})
			schemaReferenceCleanup(t, "s")
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					backend.db.SetMaxOpenConns(1)
					var actor *sql.Conn
					var executor referenceExecutor = backend.db
					reset := backend.db.ExecContext
					if !nonContext {
						var err error
						actor, err = backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer actor.Close()
						executor = actor
						reset = actor.ExecContext
					}
					run := func(s referenceStep) {
						s.nonContext = nonContext
						referenceRunStep(t, p.ctx, executor, backend.recorder, s)
					}
					run(referenceExec("CREATE USER index_schema_owner", 0))
					run(referenceExec("CREATE USER index_table_owner", 0))
					const seed = `CREATE SCHEMA s AUTHORIZATION index_schema_owner; SET SESSION AUTHORIZATION index_table_owner; CREATE TABLE s.t(n INT,s TEXT); INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b'); CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST);`
					var dump bytes.Buffer
					if !backend.pg {
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(seed)); err != nil {
							t.Fatal("trusted split-owner replay", err)
						}
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						for _, want := range []string{`CREATE SCHEMA "s" AUTHORIZATION "index_schema_owner";`, `SET SESSION AUTHORIZATION 'index_table_owner';`, `CREATE INDEX "idx" ON "s"."t" USING btree ("n" DESC NULLS LAST, "s" ASC NULLS FIRST);`} {
							if !strings.Contains(dump.String(), want) {
								t.Fatal(want, dump.String())
							}
						}
					}
					for round := range 2 {
						if backend.pg {
							if round != 0 {
								run(referenceExec("DELETE FROM s.t WHERE n=3", 1))
							}
							run(referenceExec("DROP SCHEMA IF EXISTS s CASCADE", 0))
							run(referenceExec("CREATE SCHEMA s AUTHORIZATION index_schema_owner", 0))
							if round == 0 {
								// Incoming PG witness creates as bootstrap, then transfers ownership.
								run(referenceExec("CREATE TABLE s.t(n INT,s TEXT)", 0))
								run(referenceExec("INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3))
								run(referenceExec("CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
								run(referenceExec("ALTER TABLE s.t OWNER TO index_table_owner", 0))
								run(referenceExec("GRANT USAGE ON SCHEMA s TO index_table_owner", 0))
							} else {
								// Original PG counterpart creates under the table owner's authority.
								run(referenceExec("GRANT USAGE,CREATE ON SCHEMA s TO index_table_owner", 0))
								run(referenceExec("SET SESSION AUTHORIZATION index_table_owner", 0))
								defer reset(p.ctx, "RESET SESSION AUTHORIZATION")
								run(referenceExec("CREATE TABLE s.t(n INT,s TEXT)", 0))
								run(referenceExec("INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3))
								run(referenceExec("CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
								run(referenceExec("RESET SESSION AUTHORIZATION", 0))
								run(referenceExec("REVOKE ALL ON SCHEMA s FROM index_table_owner", 0))
							}
						} else {
							if err := stuntgres.Reset(p.ctx, p.native); err != nil {
								t.Fatal(err)
							}
							if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
								t.Fatal(err)
							}
						}
						login := "postgres"
						if backend.pg {
							login = "stuntgres"
						}
						run(referenceSelect("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{login}}))
						run(referenceSelect("SELECT n,s FROM s.t ORDER BY n DESC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}, {nil, nil}}))
						run(referenceExec("SET SESSION AUTHORIZATION index_table_owner", 0))
						defer reset(p.ctx, "RESET SESSION AUTHORIZATION")
						run(referenceImplicitError("CREATE INDEX denied ON s.t(n)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema s"}))
						run(referenceExec("RESET SESSION AUTHORIZATION", 0))
						run(referenceExec("INSERT INTO s.t VALUES(3,'c')", 1))
						run(referenceSelect("SELECT n FROM s.t ORDER BY n DESC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}))
					}
					run(referenceExec("DROP SCHEMA s CASCADE", 0))
					run(referenceExec("DROP ROLE index_table_owner", 0))
					run(referenceExec("DROP ROLE index_schema_owner", 0))
				})
			}
		})
	}
}
