package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableInsertSecurity(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			actor := fmt.Sprintf("view_insert_actor_%v", held)
			owner := fmt.Sprintf("view_insert_owner_%v", held)
			fixture.RoleReferenceCleanup(t, []string{actor, owner})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					admin := func(query string) {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(query, 0))
					}
					run := func(step fixture.Step) {
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
					}
					// INSERT RETURNING retains its existing native parser boundary.
					// Match the full diagnostic and phase used by the shared suffix fixtures.
					excludedReturning := func(query string, args ...any) {
						step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "INSERT suffix is unsupported", Position: fixture.Position(strings.Index(query, "RETURNING") + 1)}, args...)
						step.SelectRows, step.Prepared, step.ErrorStage = true, held, "execute"
						if held {
							step.ErrorStage = "prepare"
						}
						run(step)
					}
					for _, query := range []string{
						"CREATE USER " + actor,
						"CREATE USER " + owner,
						"CREATE TABLE insert_base(id INT,a INT,hidden INT DEFAULT 1)",
						"CREATE VIEW insert_view AS SELECT id,a,a+hidden AS calc FROM insert_base WHERE hidden>0",
						"CREATE VIEW insert_invoker WITH(security_invoker=true) AS SELECT id,a,a+hidden AS calc FROM insert_base WHERE hidden>0",
						"GRANT INSERT(id,a) ON insert_base TO " + owner,
						"ALTER VIEW insert_view OWNER TO " + owner,
						"GRANT INSERT(id,a) ON insert_view,insert_invoker TO " + actor,
					} {
						admin(query)
					}
					// CREATE SCHEMA in PG fixture reset omits initdb's PUBLIC schema
					// rights. Normalize lookup only, not relation or column authority.
					if backend.PG {
						admin("GRANT USAGE ON SCHEMA public TO " + actor)
						admin("GRANT USAGE,CREATE ON SCHEMA public TO " + owner)
					}
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					// Neither caller nor owner has SELECT. WHERE and unused calc do
					// not make INSERT without RETURNING demand base SELECT authority.
					insert := "INSERT INTO insert_view(id,a) VALUES($1,$2)"
					var insertExecutor fixture.Executor = conn
					if held {
						stmt, err := conn.PrepareContext(p.Ctx, insert)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						insertExecutor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: insert}
					}
					fixture.RunStep(t, p.Ctx, insertExecutor, backend.Recorder, fixture.Exec(insert, 1, 1, 10))
					admin("GRANT SELECT(id,calc) ON insert_view TO " + actor)
					returning := "INSERT INTO insert_view(id,a) VALUES($1,$2) RETURNING id,calc"
					var returningExecutor fixture.Executor = conn
					if backend.PG && held {
						stmt, err := conn.PrepareContext(p.Ctx, returning)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						returningExecutor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: returning}
					}
					if backend.PG {
						denied := fixture.ImplicitError(returning, fixture.Diagnostic{Code: "42501", Message: "permission denied for table insert_base"}, 2, 20)
						denied.SelectRows, denied.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, returningExecutor, backend.Recorder, denied)
					} else {
						excludedReturning(returning, 2, 20)
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT id,a,hidden FROM insert_base", []string{"id", "a", "hidden"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1)}}))
					admin("GRANT SELECT ON insert_base TO " + owner)
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					if backend.PG {
						fixture.RunStep(t, p.Ctx, returningExecutor, backend.Recorder, fixture.Select(returning, []string{"id", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(21)}}, 2, 20))
					} else {
						// The supported command route preserves the same independent
						// committed base state without adding INSERT RETURNING.
						fixture.RunStep(t, p.Ctx, insertExecutor, backend.Recorder, fixture.Exec(insert, 1, 2, 20))
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT id,a,hidden FROM insert_base ORDER BY id", []string{"id", "a", "hidden"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), int64(20), int64(1)}}))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))

					// Invoker chooses original actor, not the ordinary view's owner.
					invokerInsert := "INSERT INTO insert_invoker(id,a) VALUES(3,30)"
					invokerDenied := fixture.ImplicitError(invokerInsert, fixture.Diagnostic{Code: "42501", Message: "permission denied for table insert_base"})
					invokerDenied.Prepared, invokerDenied.ErrorStage = held, "execute"
					run(invokerDenied)
					admin("GRANT INSERT(id,a) ON insert_base TO " + actor)
					invokerSuccess := fixture.Exec(invokerInsert, 1)
					invokerSuccess.Prepared = held
					run(invokerSuccess)
					admin("GRANT SELECT(id,calc) ON insert_invoker TO " + actor)
					invokerReturning := "INSERT INTO insert_invoker(id,a) VALUES(4,40) RETURNING id,calc"
					if backend.PG {
						denied := fixture.ImplicitError(invokerReturning, fixture.Diagnostic{Code: "42501", Message: "permission denied for table insert_base"})
						denied.SelectRows, denied.Prepared, denied.ErrorStage = true, held, "execute"
						run(denied)
					} else {
						excludedReturning(invokerReturning)
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT id,a,hidden FROM insert_base ORDER BY id", []string{"id", "a", "hidden"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), int64(20), int64(1)}, {int64(3), int64(30), int64(1)}}))
				})
			}
		})
	}
}
