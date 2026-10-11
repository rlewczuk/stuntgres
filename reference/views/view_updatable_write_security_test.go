package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableWriteSecurity(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("filtered=%v/held=%v", filtered, held), func(t *testing.T) {
				actor := fmt.Sprintf("view_write_acl_actor_%v_%v", filtered, held)
				owner := fmt.Sprintf("view_write_acl_owner_%v_%v", filtered, held)
				fixture.RoleReferenceCleanup(t, []string{actor, owner})
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						conn, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						admin := func(step fixture.Step) {
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
						}
						run := func(step fixture.Step) {
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
						}
						admin(fixture.Exec("CREATE USER "+actor, 0))
						admin(fixture.Exec("CREATE USER "+owner, 0))
						// PG reset recreates public without initdb PUBLIC privileges.
						// Normalize namespace lookup, not relation SELECT authority.
						if backend.PG {
							admin(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
							admin(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+owner, 0))
						}
						admin(fixture.Exec("CREATE TABLE write_base(id INT,a INT,hidden INT)", 0))
						admin(fixture.Exec("INSERT INTO write_base VALUES(1,10,1),(2,20,0)", 2))
						definition := "CREATE VIEW write_view AS SELECT id,a FROM write_base"
						if filtered {
							definition += " WHERE hidden>0"
						}
						admin(fixture.Exec(definition, 0))
						admin(fixture.Exec("GRANT UPDATE(a),DELETE ON write_base TO "+owner, 0))
						admin(fixture.Exec("ALTER VIEW write_view OWNER TO "+owner, 0))
						admin(fixture.Exec("GRANT UPDATE(a),DELETE ON write_view TO "+actor, 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
						// Release-source expectations were observed on the pinned
						// oracle: literal SET and constant user predicates read no
						// logical view columns. Neither identity needs SELECT, even
						// when the view's own WHERE reads an unexposed base column.
						write := func(query string, count int64) {
							var executor fixture.Executor = conn
							if held {
								stmt, err := conn.PrepareContext(p.Ctx, query)
								if err != nil {
									t.Fatalf("write authority Prepare %s: %#v", query, fixture.ExtractDiagnostic(t, err))
								}
								defer stmt.Close()
								executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
							}
							result, err := executor.ExecContext(p.Ctx, query)
							if err != nil {
								t.Fatalf("write authority Execute %s: %#v", query, fixture.ExtractDiagnostic(t, err))
							}
							fixture.LockAffected(t, result, count)
							if _, err := result.LastInsertId(); err == nil {
								t.Fatal("LastInsertId unexpectedly supported")
							}
						}
						count := int64(2)
						if filtered {
							count = 1
						}
						write("UPDATE write_view SET a=99 WHERE TRUE", count)
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						second := int64(99)
						if filtered {
							second = 20
						}
						run(fixture.Select("SELECT id,a,hidden FROM write_base ORDER BY id", []string{"id", "a", "hidden"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(99), int64(1)}, {int64(2), second, int64(0)}}))
						run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
						write("DELETE FROM write_view WHERE TRUE", count)
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						var remaining [][]any
						if filtered {
							remaining = [][]any{{int64(2), int64(20), int64(0)}}
						}
						run(fixture.Select("SELECT id,a,hidden FROM write_base ORDER BY id", []string{"id", "a", "hidden"}, []string{"INT4", "INT4", "INT4"}, remaining))
					})
				}
			})
		}
	}
}
