package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesDependencies(t *testing.T) {
	t.Run("rewire-and-cascade", func(t *testing.T) {
		transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			transactionalRulesSetup(base)
			base(fixture.Exec(transactionalRulesCreate, 0))
			oid := transactionalRulesOID(t, p, c, "r")
			handles := transactionalRulesHandles(t, p, c, held, transactionalRulesReplace)
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, handles)
			run(fixture.Exec("SAVEPOINT original", 0))
			run(fixture.Exec(transactionalRulesReplace, 0))
			run(transactionalRulesEdge("sink1", 0))
			run(transactionalRulesEdge("sink2", 1))
			transactionalRulesSameOID(t, p, e, "r", oid)
			run(fixture.Exec("DROP TABLE sink1 RESTRICT", 0))
			run(fixture.Exec("SAVEPOINT restrict_failure", 0))
			run(fixture.ImplicitError("DROP TABLE sink2 RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table sink2 because other objects depend on it", Detail: "rule r on table src depends on table sink2", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
			run(fixture.Exec("ROLLBACK TO restrict_failure", 0))
			run(fixture.Exec("INSERT INTO src VALUES(1,20)", 1))
			run(transactionalRulesRows("sink2", []any{int64(1), int64(21)}))
			run(fixture.Exec("DROP TABLE sink2 CASCADE", 0))
			run(transactionalRulesNames())
			run(transactionalRulesRows("src", []any{int64(1), int64(20)}))
			run(fixture.Exec("ROLLBACK TO original", 0))
			run(transactionalRulesNames("r"))
			transactionalRulesSameOID(t, p, e, "r", oid)
			run(transactionalRulesEdge("sink1", 1))
			run(transactionalRulesEdge("sink2", 0))
			run(transactionalRulesRows("src"))
			run(fixture.Exec("INSERT INTO src VALUES(2,-30)", 1))
			run(transactionalRulesRows("sink1", []any{int64(2), int64(30)}))
			finish(true)
			observe := transactionalRulesRun(t, p, b, peer, nil)
			observe(transactionalRulesNames("r"))
			observe(transactionalRulesRows("sink1", []any{int64(2), int64(30)}))
			observe(fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='other'::regclass", []string{"rulename"}, []string{"NAME"}, [][]any{{"r"}}))
		})
	})
	for _, kind := range []string{"target-column", "query-source", "condition-source", "constraint", "modifier-target"} {
		t.Run(kind, func(t *testing.T) {
			transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				transactionalRulesSetup(base)
				base(fixture.Exec("CREATE TABLE provider(id INT CONSTRAINT provider_key UNIQUE,v INT)", 0))
				base(fixture.Exec("INSERT INTO provider VALUES(1,7)", 1))
				q := "CREATE RULE r AS ON INSERT TO src DO INSERT INTO provider(id,v) VALUES(NEW.id,abs(NEW.v))"
				drop := "ALTER TABLE provider DROP COLUMN v CASCADE"
				switch kind {
				case "query-source":
					q = "CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink1 SELECT NEW.id,v FROM provider"
					drop = "DROP TABLE provider CASCADE"
				case "condition-source":
					q = "CREATE RULE r AS ON INSERT TO src WHERE NEW.id IN (SELECT id FROM provider) DO INSERT INTO sink1 VALUES(NEW.id,abs(NEW.v))"
					drop = "ALTER TABLE provider DROP COLUMN id CASCADE"
				case "constraint":
					q = "CREATE RULE r AS ON INSERT TO src DO INSERT INTO provider VALUES(NEW.id,NEW.v) ON CONFLICT ON CONSTRAINT provider_key DO NOTHING"
					drop = "ALTER TABLE provider DROP CONSTRAINT provider_key CASCADE"
				case "modifier-target":
					q = "CREATE RULE r AS ON INSERT TO src DO WITH changed AS (INSERT INTO provider VALUES(1,7) RETURNING v) INSERT INTO sink1 SELECT 1,v FROM changed"
					drop = "DROP TABLE provider CASCADE"
				}
				handles := transactionalRulesHandles(t, p, c, held, q)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, handles)
				run(fixture.Exec(q, 0))
				run(fixture.Exec("SAVEPOINT dependency", 0))
				run(fixture.Exec(drop, 0))
				run(transactionalRulesNames())
				run(fixture.Exec("ROLLBACK TO dependency", 0))
				run(transactionalRulesNames("r"))
				if kind != "modifier-target" {
					run(fixture.Exec("INSERT INTO src VALUES(2,-10)", 1))
				} else {
					run(fixture.Exec("DELETE FROM provider", 1))
					run(fixture.Exec("INSERT INTO src VALUES(2,-10)", 1))
					run(transactionalRulesRows("sink1", []any{int64(1), int64(7)}))
				}
				finish(false)
				base(transactionalRulesNames())
				base(transactionalRulesRows("src"))
			})
		})
	}
	t.Run("provider-retirement-waits", func(t *testing.T) {
		transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			transactionalRulesSetup(base)
			base(fixture.Exec("CREATE TABLE unrelated(id INT)", 0))
			handles := transactionalRulesHandles(t, p, c, held, transactionalRulesCreate)
			pid, hpid := 0, 0
			var oid int64
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, peer)
				hpid = fixture.LockPID(t, p.Ctx, c)
				oid = fixture.LockOID(t, p.Ctx, "sink1")
			}
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, handles)
			run(fixture.Exec(transactionalRulesCreate, 0))
			run(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO unrelated VALUES(99)", 1))
			done := make(chan struct{})
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(done)
				r, err := peer.ExecContext(ctx, "DROP TABLE sink1 CASCADE")
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
			finish(true)
			out := join()
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			base(transactionalRulesNames())
			base(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
			base(fixture.Select("SELECT id FROM unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
			base(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
		})
	})
}
