package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReview4AuthorityRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, held) })
			}
		})
	}
}

func TestReferenceRuleReview4InvokerViewAuthority(t *testing.T) {
	for _, action := range []string{"update", "delete"} {
		t.Run(action, func(t *testing.T) {
			var roles []string
			for _, held := range []bool{false, true} {
				for _, role := range []string{"actor", "owner"} {
					roles = append(roles, fmt.Sprintf("review4_view_%s_%s_%v", action, role, held))
				}
			}
			fixture.RoleReferenceCleanup(t, roles)
			ruleReview4AuthorityRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				actor := fmt.Sprintf("review4_view_%s_actor_%v", action, held)
				owner := fmt.Sprintf("review4_view_%s_owner_%v", action, held)
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				mutation := "UPDATE sink SET v=NEW.v WHERE id=OLD.id"
				privilege := "UPDATE"
				if action == "delete" {
					mutation = "DELETE FROM sink WHERE id=OLD.id"
					privilege = "DELETE"
				}
				run(fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0),
					fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE input(id INT,v INT)", 0),
					fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("INSERT INTO src VALUES(1,1)", 1),
					fixture.Exec("INSERT INTO input VALUES(1,10)", 1), fixture.Exec("INSERT INTO sink VALUES(1,5)", 1),
					fixture.Exec("CREATE VIEW input_v WITH (security_invoker=true) AS SELECT id,v FROM input", 0),
					fixture.Exec("CREATE RULE r AS ON UPDATE TO src DO ALSO "+mutation, 0),
					fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0),
					fixture.Exec("GRANT SELECT,UPDATE ON src TO "+actor, 0),
					fixture.Exec("GRANT SELECT ON input,input_v TO "+actor, 0),
					fixture.Exec("GRANT SELECT,"+privilege+" ON sink TO "+owner, 0))
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor+","+owner, 0))
				}
				// Native public is already visible; do not fabricate schema grants.
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+actor); err != nil {
					t.Fatal(err)
				}
				q := "UPDATE src SET v=i.v FROM input_v AS i WHERE src.id=i.id"
				var executor fixture.Executor = conn
				if held {
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				}
				fire := func() { fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1)) }
				deny := func(table string) {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				}
				state := func(source, input, sink int64) {
					run(ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), source}),
						ruleInts("SELECT id,v FROM input ORDER BY id", []any{int64(1), input}))
					if action == "delete" && sink == 0 {
						run(ruleInts("SELECT id,v FROM sink ORDER BY id"))
					} else {
						run(ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), sink}))
					}
				}
				// The owner can mutate sink, but cannot read input or input_v.
				fire()
				sink := int64(10)
				if action == "delete" {
					sink = 0
				}
				state(10, 10, sink)
				if action == "delete" {
					run(fixture.Exec("INSERT INTO sink VALUES(1,5)", 1))
					sink = 5
				}
				run(fixture.Exec("UPDATE input SET v=20", 1),
					fixture.Exec("GRANT SELECT ON input,input_v TO "+owner, 0),
					fixture.Exec("REVOKE SELECT ON input FROM "+actor, 0))
				// Owner input privileges cannot replace the caller's current grant.
				deny("input")
				state(10, 20, sink)
				run(fixture.Exec("GRANT SELECT ON input TO "+actor, 0))
				fire()
				sink = 20
				if action == "delete" {
					sink = 0
				}
				state(20, 20, sink)
				if action == "delete" {
					run(fixture.Exec("INSERT INTO sink VALUES(1,5)", 1))
				}
				run(fixture.Exec("UPDATE input SET v=30", 1),
					fixture.Exec("REVOKE SELECT ON input,input_v FROM "+owner, 0))
				// SAME held consumer recovers, then tolerates owner input revocation.
				fire()
				sink = 30
				if action == "delete" {
					sink = 0
				}
				state(30, 30, sink)
				if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
