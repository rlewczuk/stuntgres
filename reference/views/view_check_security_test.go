package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckSecurity(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			actor := fmt.Sprintf("check_actor_%v", held)
			owner := fmt.Sprintf("check_owner_%v", held)
			// Register before the pair: sessions and schema dependencies retire first.
			fixture.RoleReferenceCleanup(t, []string{actor, owner})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					admin := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{"CREATE USER " + actor, "CREATE USER " + owner, "CREATE TABLE base(a INT DEFAULT -1,b INT DEFAULT 10,secret TEXT DEFAULT 'hidden')", "CREATE TABLE lookup(k INT)", "CREATE VIEW v AS SELECT a,b FROM base WHERE a<b WITH LOCAL CHECK OPTION", "CREATE VIEW source_v AS SELECT a,b FROM base WHERE EXISTS(SELECT 1 FROM lookup WHERE k=base.a) WITH CHECK OPTION", "CREATE VIEW invoker_v WITH(security_invoker=true) AS SELECT a,b FROM base WHERE EXISTS(SELECT 1 FROM lookup WHERE k=base.a) WITH CHECK OPTION", "GRANT INSERT ON base TO " + owner, "ALTER VIEW v OWNER TO " + owner, "ALTER VIEW source_v OWNER TO " + owner, "GRANT INSERT ON v,source_v,invoker_v TO " + actor} {
						admin(fixture.Exec(q, 0))
					}
					if b.PG {
						admin(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
						admin(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+owner, 0))
					}
					admin(fixture.Exec("INSERT INTO lookup VALUES(1)", 1))
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					for _, query := range []string{"ALTER VIEW v SET(check_option=here)", "ALTER VIEW v RESET(check_option)"} {
						denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "must be owner of view v"})
						denied.Prepared = held
						denied.ErrorStage = "execute"
						run(denied)
					}
					q := "INSERT INTO v(a,b) VALUES($1,$2)"
					var e fixture.Executor = c
					if held {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 1, 1, 2))
					partial := viewCheckError(q, "v", "3, 2", 3, 2)
					partial.Diagnostic.Detail = "Failing row contains (a, b) = (3, 2)."
					fixture.RunStep(t, p.Ctx, e, b.Recorder, partial)
					// No assigned/permitted columns: DEFAULT VALUES fails without leaking defaults.
					admin(fixture.Exec("CREATE VIEW default_v AS SELECT a,b FROM base WHERE a>0 WITH CHECK OPTION", 0))
					admin(fixture.Exec("ALTER VIEW default_v OWNER TO "+owner, 0))
					admin(fixture.Exec("GRANT INSERT ON default_v TO "+actor, 0))
					empty := viewCheckError("INSERT INTO default_v DEFAULT VALUES", "default_v", "")
					empty.Diagnostic.Detail = ""
					empty.Prepared = held
					run(empty)
					admin(fixture.Exec("GRANT SELECT ON base TO "+actor, 0))
					full := viewCheckError(q, "v", "3, 2, hidden", 3, 2)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, full)
					admin(fixture.Exec("REVOKE SELECT ON base FROM "+actor, 0))
					fixture.RunStep(t, p.Ctx, e, b.Recorder, partial)
					for _, name := range []string{"source_v", "invoker_v"} {
						query := "INSERT INTO " + name + " VALUES($1,$2)"
						var source fixture.Executor = c
						if held {
							stmt, err := c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							source = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
						}
						if name == "invoker_v" {
							admin(fixture.Exec("GRANT INSERT ON base TO "+actor, 0))
							admin(fixture.Exec("REVOKE SELECT ON lookup FROM "+actor, 0))
						}
						denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table lookup"}, 1, 3)
						denied.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, source, b.Recorder, denied)
						right, wrong := owner, actor
						if name == "invoker_v" {
							right, wrong = actor, owner
						}
						admin(fixture.Exec("GRANT SELECT ON lookup TO "+wrong, 0))
						fixture.RunStep(t, p.Ctx, source, b.Recorder, denied)
						admin(fixture.Exec("GRANT SELECT ON lookup TO "+right, 0))
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(query, 1, 1, 3))
						admin(fixture.Exec("REVOKE SELECT ON lookup FROM "+right, 0))
						fixture.RunStep(t, p.Ctx, source, b.Recorder, denied)
						admin(fixture.Exec("GRANT SELECT ON lookup TO "+right, 0))
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(query, 1, 1, 4))
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					admin(viewWriteBag("SELECT a,b,secret FROM base", []string{"a", "b", "secret"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "hidden"}, {int64(1), int64(3), "hidden"}, {int64(1), int64(4), "hidden"}, {int64(1), int64(3), "hidden"}, {int64(1), int64(4), "hidden"}}))
				})
			}
		})
	}
}
