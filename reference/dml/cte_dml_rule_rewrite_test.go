package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCTEDMLRuleRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, rule, query string
		names             []string
		types             []string
		rows, sink        [][]any
	}{
		{"same_event", "DELETE FROM sink WHERE id=OLD.id RETURNING id,v", "WITH d AS (DELETE FROM t WHERE id=1 RETURNING id AS out,v+1 AS next) SELECT out,next FROM d", []string{"out", "next"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}, [][]any{{int64(2), int64(20)}}},
		{"cross_event", "UPDATE sink SET v=OLD.v+100 WHERE id=OLD.id RETURNING id,v", "WITH d AS (DELETE FROM t WHERE id=1 RETURNING id AS out,v+1 AS next) SELECT out,next FROM d", []string{"out", "next"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(111)}}, [][]any{{int64(1), int64(110)}, {int64(2), int64(20)}}},
		{"unused", "DELETE FROM sink WHERE id=OLD.id RETURNING id,v", "WITH d AS (DELETE FROM t WHERE id=1) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}, [][]any{{int64(2), int64(20)}}},
		{"limit_zero", "DELETE FROM sink WHERE id=OLD.id RETURNING id,v", "WITH d AS (DELETE FROM t WHERE id=1 RETURNING id AS out,v+1 AS next) SELECT out,next FROM d LIMIT 0", []string{"out", "next"}, []string{"INT4", "INT4"}, [][]any{}, [][]any{{int64(2), int64(20)}}},
		{"repeated_snapshot", "DELETE FROM sink WHERE id=OLD.id RETURNING id,v", "WITH d AS (DELETE FROM t WHERE id=1 RETURNING id AS out,v+1 AS next) SELECT a.out,b.next,(SELECT count(*) FROM sink) AS seen FROM d a CROSS JOIN d b", []string{"out", "next", "seen"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(11), int64(2)}}, [][]any{{int64(2), int64(20)}}},
		{"materialized_aliases", "DELETE FROM sink WHERE id=OLD.id RETURNING id,v", "WITH d(x,y) AS MATERIALIZED (DELETE FROM t WHERE id=1 RETURNING id,v+1) SELECT a.x,b.y FROM d a CROSS JOIN d b", []string{"x", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}, [][]any{{int64(2), int64(20)}}},
		{"not_materialized_aliases", "DELETE FROM sink WHERE id=OLD.id RETURNING id,v", "WITH d(x,y) AS NOT MATERIALIZED (DELETE FROM t WHERE id=1 RETURNING id,v+1) SELECT a.x,b.y FROM d a CROSS JOIN d b", []string{"x", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}, [][]any{{int64(2), int64(20)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0))
				cteDMLRun(t, p, b, false, fixture.Exec("INSERT INTO sink VALUES(1,10),(2,20)", 2))
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE RULE r AS ON DELETE TO t DO INSTEAD "+tc.rule, 0))
				cteDMLRun(t, p, b, held, cteDMLBag(tc.query, tc.names, tc.types, tc.rows))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM sink", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.sink))
			})
		})
	}
	for _, tc := range []struct{ name, rule, second, message string }{
		{"nothing", "CREATE RULE r AS ON DELETE TO t DO INSTEAD NOTHING", "", "DO INSTEAD NOTHING rules are not supported for data-modifying statements in WITH"},
		{"recursive_multi", "CREATE RULE r AS ON DELETE TO t DO INSTEAD DELETE FROM sink WHERE id=OLD.id", "CREATE RULE s AS ON DELETE TO sink DO INSTEAD (DELETE FROM log WHERE id=OLD.id; INSERT INTO log VALUES(OLD.id))", "multi-statement DO INSTEAD rules are not supported for data-modifying statements in WITH"},
		{"recursive_also", "CREATE RULE r AS ON DELETE TO t DO INSTEAD DELETE FROM sink WHERE id=OLD.id", "CREATE RULE s AS ON DELETE TO sink DO ALSO INSERT INTO log VALUES(OLD.id)", "DO ALSO rules are not supported for data-modifying statements in WITH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0))
				cteDMLRun(t, p, b, false, fixture.Exec("INSERT INTO sink VALUES(1,10),(2,20)", 2))
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE log(id INT)", 0))
				cteDMLRun(t, p, b, false, fixture.Exec(tc.rule, 0))
				if tc.second != "" {
					cteDMLRun(t, p, b, false, fixture.Exec(tc.second, 0))
				}
				cteDMLError(t, p, b, held, "WITH d AS (DELETE FROM t) SELECT 7 AS n", fixture.Diagnostic{Code: "0A000", Message: tc.message})
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM sink", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id FROM log", []string{"id"}, []string{"INT4"}, [][]any{}))
			})
		})
	}
	for _, tc := range []struct {
		name, rule   string
		rows, source [][]any
	}{
		{"also_nothing", "CREATE RULE r AS ON DELETE TO t DO ALSO NOTHING", [][]any{{int64(1)}, {int64(2)}}, [][]any{}},
		{"conditional_nothing", "CREATE RULE r AS ON DELETE TO t WHERE OLD.id>1 DO INSTEAD NOTHING", [][]any{{int64(1)}}, [][]any{{int64(2), int64(20)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec(tc.rule, 0))
				if tc.name == "conditional_nothing" {
					cteDMLError(t, p, b, held, "WITH d AS (DELETE FROM t RETURNING id) SELECT id FROM d", fixture.Diagnostic{Code: "0A000", Message: "cannot perform DELETE RETURNING on relation \"t\"", Hint: "You need an unconditional ON DELETE DO INSTEAD rule with a RETURNING clause."})
					cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					cteDMLRun(t, p, b, held, fixture.Select("WITH d AS (DELETE FROM t) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
				} else {
					cteDMLRun(t, p, b, held, cteDMLBag("WITH d AS (DELETE FROM t RETURNING id) SELECT id FROM d", []string{"id"}, []string{"INT4"}, tc.rows))
				}
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.source))
			})
		})
	}
	for _, mode := range []string{"unused", "referenced", "returning"} {
		t.Run("select_"+mode, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE SEQUENCE cte_rule_seq", 0))
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE RULE r AS ON DELETE TO t DO INSTEAD SELECT nextval('cte_rule_seq') AS n", 0))
				switch mode {
				case "unused":
					cteDMLRun(t, p, b, held, cteDMLBag("WITH d AS (DELETE FROM t) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
				case "referenced":
					q := "WITH d AS (DELETE FROM t) SELECT * FROM d"
					cteDMLError(t, p, b, held, q, fixture.Diagnostic{Code: "0A000", Message: `WITH query "d" does not have a RETURNING clause`, Position: cteDMLPosition(q, "d", true)})
				case "returning":
					cteDMLError(t, p, b, held, "WITH d AS (DELETE FROM t RETURNING id) SELECT id FROM d", fixture.Diagnostic{Code: "0A000", Message: `cannot perform DELETE RETURNING on relation "t"`, Hint: "You need an unconditional ON DELETE DO INSTEAD rule with a RETURNING clause."})
				}
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT last_value,is_called FROM cte_rule_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			})
		})
	}
	t.Run("late_failure", func(t *testing.T) {
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			cteDMLSeed(t, p, b)
			for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(1,10),(2,20)", 2), fixture.Exec("CREATE TABLE log(id INT)", 0), fixture.Exec("CREATE TABLE bad(id INT NOT NULL)", 0), fixture.Exec("CREATE RULE r AS ON DELETE TO t DO INSTEAD DELETE FROM sink WHERE id=OLD.id RETURNING id,v", 0)} {
				cteDMLRun(t, p, b, false, step)
			}
			q := "WITH prior AS (INSERT INTO log VALUES(1) RETURNING id), d AS (DELETE FROM t USING prior WHERE t.id=prior.id RETURNING t.id) INSERT INTO bad SELECT NULL FROM d RETURNING id"
			cteDMLError(t, p, b, held, q, fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "bad" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "bad", Column: "id"}, "execute")
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM sink", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id FROM log", []string{"id"}, []string{"INT4"}, [][]any{}))
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id FROM bad", []string{"id"}, []string{"INT4"}, [][]any{}))
		})
	})
	t.Run("parent_with_multi", func(t *testing.T) {
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			cteDMLSeed(t, p, b)
			for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT)", 0), fixture.Exec("CREATE RULE r AS ON DELETE TO t DO ALSO INSERT INTO log VALUES(OLD.id)", 0)} {
				cteDMLRun(t, p, b, false, step)
			}
			cteDMLError(t, p, b, held, "WITH prior AS (INSERT INTO sink VALUES(3,30)) DELETE FROM t WHERE id=1", fixture.Diagnostic{Code: "0A000", Message: "WITH cannot be used in a query that is rewritten by rules into multiple queries"})
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM sink", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id FROM log", []string{"id"}, []string{"INT4"}, [][]any{}))
		})
	})
	t.Run("prepared_owner_invalidation", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"cte_rule_actor_false", "cte_rule_owner_false", "cte_rule_actor_true", "cte_rule_owner_true"})
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			actor, owner := fmt.Sprintf("cte_rule_actor_%v", held), fmt.Sprintf("cte_rule_owner_%v", held)
			cteDMLSeed(t, p, b)
			for _, step := range []fixture.Step{fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(1,10),(2,20)", 2), fixture.Exec("CREATE TABLE sink2(id INT,v INT)", 0), fixture.Exec("INSERT INTO sink2 VALUES(2,200)", 1), fixture.Exec("CREATE RULE r AS ON DELETE TO t DO INSTEAD DELETE FROM sink WHERE id=OLD.id RETURNING id,v", 0), fixture.Exec("GRANT DELETE,SELECT ON sink,sink2 TO "+owner, 0), fixture.Exec("ALTER TABLE t OWNER TO "+owner, 0), fixture.Exec("GRANT DELETE,SELECT ON t TO "+actor, 0)} {
				cteDMLRun(t, p, b, false, step)
			}
			if b.PG {
				cteDMLRun(t, p, b, false, fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor+","+owner, 0))
			}
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+actor); err != nil {
				t.Fatal(err)
			}
			q := "WITH d AS (DELETE FROM t WHERE id=$1 RETURNING id AS out,v+1 AS next) SELECT out,next FROM d"
			var executor fixture.Executor = conn
			if held {
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			}
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"out", "next"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}, 1))
			cteDMLRun(t, p, b, false, fixture.Exec("REVOKE DELETE ON sink FROM "+owner, 0))
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table sink"}, 2)
			failure.SelectRows = true
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, failure)
			cteDMLRun(t, p, b, false, fixture.Exec("CREATE OR REPLACE RULE r AS ON DELETE TO t DO INSTEAD DELETE FROM sink2 WHERE id=OLD.id RETURNING id,v", 0))
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"out", "next"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(201)}}, 2))
			if _, err = conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM sink", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM sink2", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
		})
	})
}
