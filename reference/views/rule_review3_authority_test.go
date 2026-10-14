package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReview3AuthorityRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
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

func TestReferenceRuleReview3IndependentAuthority(t *testing.T) {
	for _, event := range []string{"UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			roles := []string{}
			for _, held := range []bool{false, true} {
				for _, role := range []string{"actor", "owner", "successor"} {
					roles = append(roles, fmt.Sprintf("review3_ind_%s_%s_%v", event, role, held))
				}
			}
			fixture.RoleReferenceCleanup(t, roles)
			ruleReview3AuthorityRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				actor := fmt.Sprintf("review3_ind_%s_actor_%v", event, held)
				owner := fmt.Sprintf("review3_ind_%s_owner_%v", event, held)
				successor := fmt.Sprintf("review3_ind_%s_successor_%v", event, held)
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0), fixture.Exec("CREATE USER "+successor, 0),
					fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE lookup(v INT)", 0), fixture.Exec("CREATE TABLE log(v INT)", 0),
					fixture.Exec("INSERT INTO lookup VALUES(7)", 1), fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO ALSO INSERT INTO log SELECT v FROM lookup", 0),
					fixture.Exec("GRANT SELECT ON lookup TO "+owner, 0), fixture.Exec("GRANT INSERT ON log TO "+owner, 0),
					fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0), fixture.Exec("GRANT "+event+" ON src TO "+actor, 0))
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor+","+owner+","+successor, 0))
				}
				// Native public is already visible; no fictitious schema USAGE grant.
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+actor); err != nil {
					t.Fatal(err)
				}
				q := "DELETE FROM src"
				if event == "UPDATE" {
					q = "UPDATE src SET v=10"
				}
				var executor fixture.Executor = conn
				if held {
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				}
				fire := func() { fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0)) }
				deny := func(table string) {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
					if held {
						s.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				}
				fire()
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(7)}), ruleInts("SELECT id,v FROM src"),
					fixture.Exec("GRANT SELECT ON lookup TO "+actor, 0), fixture.Exec("REVOKE SELECT ON lookup FROM "+owner, 0))
				deny("lookup")
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(7)}), ruleInts("SELECT id,v FROM src"),
					fixture.Exec("GRANT SELECT ON lookup TO "+owner, 0), fixture.Exec("REVOKE SELECT ON lookup FROM "+actor, 0))
				fire()
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(7)}, []any{int64(7)}), fixture.Exec("ALTER TABLE src OWNER TO "+successor, 0))
				// SAME held consumer observes the new event owner, not the rule creator.
				deny("log")
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(7)}, []any{int64(7)}), ruleInts("SELECT id,v FROM src"),
					fixture.Exec("GRANT SELECT ON lookup TO "+successor, 0), fixture.Exec("GRANT INSERT ON log TO "+successor, 0))
				fire()
				if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(7)}, []any{int64(7)}, []any{int64(7)}), ruleInts("SELECT id,v FROM src"))
			})
		})
	}
}

func TestReferenceRuleReview3InheritedAuthority(t *testing.T) {
	for _, event := range []string{"UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			roles := []string{}
			for _, held := range []bool{false, true} {
				for _, role := range []string{"actor", "owner"} {
					roles = append(roles, fmt.Sprintf("review3_dep_%s_%s_%v", event, role, held))
				}
			}
			fixture.RoleReferenceCleanup(t, roles)
			ruleReview3AuthorityRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				actor := fmt.Sprintf("review3_dep_%s_actor_%v", event, held)
				owner := fmt.Sprintf("review3_dep_%s_owner_%v", event, held)
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0), fixture.Exec("CREATE TABLE src(id INT,v INT)", 0),
					fixture.Exec("CREATE TABLE input(id INT,v INT)", 0), fixture.Exec("CREATE TABLE lookup(v INT)", 0), fixture.Exec("CREATE TABLE log(v INT)", 0),
					fixture.Exec("INSERT INTO src VALUES(1,5)", 1), fixture.Exec("INSERT INTO input VALUES(1,10)", 1), fixture.Exec("INSERT INTO lookup VALUES(7)", 1),
					fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO ALSO INSERT INTO log SELECT OLD.id+v FROM lookup", 0),
					fixture.Exec("GRANT SELECT ON lookup TO "+owner, 0), fixture.Exec("GRANT INSERT ON log TO "+owner, 0), fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0),
					fixture.Exec("GRANT "+event+",SELECT ON src TO "+actor, 0), fixture.Exec("GRANT SELECT ON input TO "+actor, 0))
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor+","+owner, 0))
				}
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+actor); err != nil {
					t.Fatal(err)
				}
				q := "DELETE FROM src USING input WHERE src.id=input.id"
				if event == "UPDATE" {
					q = "UPDATE src SET v=input.v FROM input WHERE src.id=input.id"
				}
				var executor fixture.Executor = conn
				if held {
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1))
				if event == "DELETE" {
					run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
				}
				run(fixture.Exec("GRANT SELECT ON input TO "+owner, 0), fixture.Exec("REVOKE SELECT ON input FROM "+actor, 0))
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table input"})
				if held {
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(8)}), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}),
					fixture.Exec("GRANT SELECT ON input TO "+actor, 0), fixture.Exec("REVOKE SELECT ON input FROM "+owner, 0))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1))
				if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
				run(ruleRows("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"INT4"}, []any{int64(8)}, []any{int64(8)}), ruleInts("SELECT id,v FROM input", []any{int64(1), int64(10)}))
				if event == "UPDATE" {
					run(ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}))
				} else {
					run(ruleInts("SELECT id,v FROM src"))
				}
			})
		})
	}
}
