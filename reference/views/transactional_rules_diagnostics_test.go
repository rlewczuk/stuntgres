package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesDiagnostics(t *testing.T) {
	cases := []struct{ q, code, message, position string }{
		{transactionalRulesCreate, "42710", "rule \"r\" for relation \"src\" already exists", ""},
		{"ALTER RULE missing ON src RENAME TO absent", "42704", "rule \"missing\" for relation \"src\" does not exist", ""},
		{"DROP RULE missing ON src", "42704", "rule \"missing\" for relation \"src\" does not exist", ""},
		{"CREATE RULE missing AS ON INSERT TO absent DO NOTHING", "42P01", "relation \"absent\" does not exist", ""},
		{"ALTER RULE missing ON absent RENAME TO later", "42P01", "relation \"absent\" does not exist", ""},
		{"DROP RULE missing ON absent", "42P01", "relation \"absent\" does not exist", ""},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			transactionalRulesRoutes(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				transactionalRulesSetup(base)
				base(fixture.Exec(transactionalRulesCreate, 0))
				handles := transactionalRulesHandles(t, p, c, held, tc.q)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, handles)
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.ErrorStage = "execute"
				run(s)
				if mode != "autocommit" {
					run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				}
				finish(false)
				base(transactionalRulesNames("r"))
				base(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
				base(transactionalRulesRows("sink1", []any{int64(1), int64(10)}))
			})
		})
	}
	for _, q := range []string{transactionalRulesCreate, transactionalRulesReplace, transactionalRulesRename, transactionalRulesDrop} {
		t.Run("read-only/"+q, func(t *testing.T) {
			transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				transactionalRulesSetup(base)
				base(fixture.Exec(transactionalRulesCreate, 0))
				handles := transactionalRulesHandles(t, p, c, held, q)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, handles)
				run(fixture.Exec("SET TRANSACTION READ ONLY", 0))
				tag := "CREATE RULE"
				if q == transactionalRulesRename {
					tag = "ALTER RULE"
				}
				if q == transactionalRulesDrop {
					tag = "DROP RULE"
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tag + " in a read-only transaction"})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				finish(false)
				base(transactionalRulesNames("r"))
			})
		})
	}
	t.Run("conditional-notice-phase", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
				run(fixture.Exec("CREATE TABLE src(id INT)", 0))
				q := "DROP RULE IF EXISTS missing ON src"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				absent := "DROP RULE IF EXISTS missing ON absent"
				missing, err := c.PrepareContext(p.Ctx, absent)
				if err != nil {
					t.Fatal(err)
				}
				defer missing.Close()
				fixture.RegtypeAssertNotices(t, notices, nil)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, rec, fixture.Exec(q, 0))
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "rule \"missing\" for relation \"src\" does not exist, skipping"}})
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: missing, SQL: absent}, rec, fixture.Exec(absent, 0))
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "rule \"missing\" for relation \"src\" does not exist, skipping"}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "relation \"absent\" does not exist, skipping"}})
				run(fixture.Exec("COMMIT", 0))
			})
		}
	})
	for family, q := range []string{transactionalRulesCreate, transactionalRulesReplace, transactionalRulesRename, transactionalRulesDrop} {
		t.Run(fmt.Sprintf("held-current-owner/%d", family), func(t *testing.T) {
			names := []string{}
			for _, mode := range []string{"autocommit", "sql", "go"} {
				for _, held := range []bool{false, true} {
					names = append(names, fmt.Sprintf("txr_actor_%d_%s_%v", family, mode, held), fmt.Sprintf("txr_owner_%d_%s_%v", family, mode, held))
				}
			}
			fixture.RoleReferenceCleanup(t, names)
			transactionalRulesRoutes(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				actor := fmt.Sprintf("txr_actor_%d_%s_%v", family, mode, held)
				owner := fmt.Sprintf("txr_owner_%d_%s_%v", family, mode, held)
				base := transactionalRulesRun(t, p, b, c, nil)
				transactionalRulesSetup(base)
				if family > 0 {
					base(fixture.Exec(transactionalRulesCreate, 0))
				}
				if family == 3 {
					base(fixture.Exec(transactionalRulesRename, 0))
				}
				for _, setup := range []string{"CREATE USER " + actor, "CREATE USER " + owner, "GRANT USAGE ON SCHEMA public TO " + actor + "," + owner, "GRANT INSERT ON sink1,sink2 TO " + actor + "," + owner, "ALTER TABLE src OWNER TO " + actor} {
					base(fixture.Exec(setup, 0))
				}
				handles := transactionalRulesHandles(t, p, c, held, q)
				base(fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0))
				base(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, handles)
				message := "must be owner of table src"
				if family == 3 {
					message = "must be owner of relation src"
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message})
				s.ErrorStage = "execute"
				run(s)
				finish(false)
				base(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				base(fixture.Exec("ALTER TABLE src OWNER TO "+actor, 0))
				base(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
				e, finish = transactionalViewsBegin(t, p, b, c, mode)
				run = transactionalRulesRun(t, p, b, e, handles)
				run(fixture.Exec(q, 0))
				run(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
				finish(true)
				var a, z [][]any
				if family == 1 {
					z = [][]any{{int64(1), int64(-9)}}
				} else if family != 3 {
					a = [][]any{{int64(1), int64(10)}}
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalRulesRows("sink1", a...))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalRulesRows("sink2", z...))
			})
		})
	}
}
