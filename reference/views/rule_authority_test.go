package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRulePrivileges(t *testing.T) {
	// Parent cleanup outlives all per-route fixture resets and role-owned objects.
	fixture.RoleReferenceCleanup(t, []string{"rule_actor_false", "rule_owner_false", "rule_actor_true", "rule_owner_true"})
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		actor := fmt.Sprintf("rule_actor_%v", held)
		owner := fmt.Sprintf("rule_owner_%v", held)
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0), fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), fixture.Exec("GRANT INSERT ON sink TO "+owner, 0), fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0), fixture.Exec("GRANT INSERT ON src TO "+actor, 0))
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
		q := "INSERT INTO src VALUES($1,$2)"
		var executor fixture.Executor = conn
		if held {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 1, 10))
		run(fixture.Exec("REVOKE INSERT ON sink FROM "+owner, 0))
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table sink"}, 2, 20)
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		run(fixture.Exec("GRANT INSERT ON sink TO "+owner, 0))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 3, 30))
		if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Fatal(err)
		}
		run(ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}))
	})
}

func TestReferenceRuleBoundaries(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "CREATE RULE r AS ON INSERT TO src DO INSTEAD NOTHING"
		var x fixture.Executor = conn
		if held {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("BEGIN READ ONLY", 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE RULE in a read-only transaction"}))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_rule", 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec("INSERT INTO src VALUES(1,10)", 0))
		run(ruleInts("SELECT id,v FROM src"))
		run(fixture.ImplicitError("SELECT 1/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO before_rule", 0))
		run(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
		run(ruleInts("SELECT id,v FROM src", []any{int64(2), int64(20)}))
		run(fixture.Exec("COMMIT", 0))
		ruleRun(t, p, b, held, ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}))
		if !b.PG {
			ruleRun(t, p, b, held, ruleError("CREATE RULE transport AS ON INSERT TO src DO NOTIFY rule_channel", "0A000", "NOTIFY is unsupported"), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}))
		}
	})
}
