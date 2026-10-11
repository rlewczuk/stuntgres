package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Keep these public regressions separate from implementation-mechanism tests.
func TestReferenceViewUpdatableReviewConversion(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(hidden INT DEFAULT 7,a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(5,10)", 1))
		run(fixture.Exec("CREATE VIEW v AS SELECT a AS x FROM t", 0))
		query := "INSERT INTO v(x) VALUES(CAST($1 AS BIGINT))"
		var executor fixture.Executor = b.DB
		if held {
			executor = fixture.ViewHold(t, p, b, query)
		}
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, int64(2147483648))
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		run(fixture.Select("SELECT hidden,a FROM t ORDER BY a", []string{"hidden", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(10)}}))
		// A valid execution on that same retained handle must remain usable.
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(query, 1, int64(11)))
		run(fixture.Select("SELECT hidden,a FROM t ORDER BY a", []string{"hidden", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(10)}, {int64(7), int64(11)}}))
	})
}

func TestReferenceViewUpdatableReviewSubqueryACL(t *testing.T) {
	for _, invoker := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("invoker=%v/held=%v", invoker, held), func(t *testing.T) {
				actor := fmt.Sprintf("view_review_actor_%v_%v", invoker, held)
				owner := fmt.Sprintf("view_review_owner_%v_%v", invoker, held)
				fixture.RoleReferenceCleanup(t, []string{actor, owner})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						admin := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
						admin(fixture.Exec("CREATE USER "+actor, 0))
						admin(fixture.Exec("CREATE USER "+owner, 0))
						if b.PG {
							admin(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
							admin(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+owner, 0))
						}
						admin(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
						admin(fixture.Exec("CREATE TABLE secret(flag INT)", 0))
						admin(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
						admin(fixture.Exec("INSERT INTO secret VALUES(1)", 1))
						options := ""
						if invoker {
							options = " WITH(security_invoker=true)"
						}
						admin(fixture.Exec("CREATE VIEW v"+options+" AS SELECT id,a FROM t WHERE EXISTS(SELECT 1 FROM secret WHERE flag=1)", 0))
						admin(fixture.Exec("GRANT UPDATE(a) ON t TO "+owner, 0))
						admin(fixture.Exec("ALTER VIEW v OWNER TO "+owner, 0))
						admin(fixture.Exec("GRANT UPDATE(a) ON v TO "+actor, 0))
						identity := owner
						if invoker {
							identity = actor
							admin(fixture.Exec("GRANT UPDATE(a) ON t TO "+actor, 0))
						}
						// Neither identity gets SELECT on the main target. Independent
						// subquery SELECT is checked as current owner/original invoker.
						run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
						query := "UPDATE v SET a=99 WHERE TRUE"
						var executor fixture.Executor = conn
						if held {
							stmt, err := conn.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatalf("Prepare: %#v", fixture.ExtractDiagnostic(t, err))
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
						}
						denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"})
						denied.ErrorStage = "execute"
						checkBase := func(a, second int64) {
							admin(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), a}, {int64(2), second}}))
						}
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, denied)
						checkBase(10, 20)
						// Granting the wrong identity cannot repair the independent ACL.
						other := actor
						if invoker {
							other = owner
						}
						admin(fixture.Exec("GRANT SELECT ON secret TO "+other, 0))
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, denied)
						checkBase(10, 20)
						admin(fixture.Exec("GRANT SELECT ON secret TO "+identity, 0))
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(query, 2))
						checkBase(99, 99)
						admin(fixture.Exec("UPDATE t SET a=id*10", 2))
						admin(fixture.Exec("REVOKE SELECT ON secret FROM "+identity, 0))
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, denied)
						checkBase(10, 20)
						admin(fixture.Exec("GRANT SELECT ON secret TO "+identity, 0))
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(query, 2))
						checkBase(99, 99)
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					})
				}
			})
		}
	}
}

func TestReferenceViewUpdatableReviewSubqueryVisibility(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
		run(fixture.Exec("CREATE TABLE q(k INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO q VALUES(7),(8)", 2))
		run(fixture.Exec("CREATE VIEW v AS SELECT id,a,(SELECT count(*) FROM q) AS n FROM t", 0))
		write := "UPDATE v SET a=CAST(n AS INT)"
		returning := write + " RETURNING id,a,n"
		var command, rows fixture.Executor = conn, conn
		if held {
			for _, target := range []struct {
				query    string
				executor *fixture.Executor
			}{{write, &command}, {returning, &rows}} {
				stmt, err := conn.PrepareContext(p.Ctx, target.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				*target.executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: target.query}
			}
		}
		run(fixture.Exec("ALTER TABLE q ADD COLUMN extra INT", 0))
		run(fixture.Exec("DELETE FROM q", 2))
		check := func(count int64) {
			fixture.RunStep(t, p.Ctx, command, b.Recorder, fixture.Exec(write, 1))
			fixture.RunStep(t, p.Ctx, rows, b.Recorder, fixture.Select(returning, []string{"id", "a", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), count, count}}))
			run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), count}}))
			run(fixture.Select("SELECT count(*) FROM q", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
		}
		check(0)
		run(fixture.Exec("BEGIN", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("INSERT INTO q(k) VALUES(9),(10)", 2))
		check(2)
		run(fixture.Exec("UPDATE q SET k=11 WHERE k=9", 1))
		check(2)
		run(fixture.Exec("DELETE FROM q WHERE k=10", 1))
		check(1)
		run(fixture.Exec("DELETE FROM q", 1))
		check(0)
		run(fixture.Exec("ROLLBACK", 0))
		check(0)
		run(fixture.Select("SELECT k,extra FROM q", []string{"k", "extra"}, []string{"INT4", "INT4"}, nil))
	})
}
