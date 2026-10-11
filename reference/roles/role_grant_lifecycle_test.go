package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleGrantTransactions(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		q := "GRANT SELECT ON t TO rg_reader"
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			run(fixture.Exec("BEGIN", 0))
			if b.PG {
				run(fixture.Exec(q, 0))
				run(fixture.Exec(end, 0))
			} else {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				// The unsupported utility fails the ordinary native transaction.
				s = fixture.ImplicitError("SELECT a FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				s.SelectRows = true
				run(s)
				run(fixture.Exec("ROLLBACK", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			if b.PG && end == "COMMIT" {
				run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			} else {
				run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, command := range []string{"GRANT SELECT ON missing TO missing_role", "REVOKE SELECT ON missing FROM missing_role"} {
			// Execute read-only precedence is separate from the native Prepare
			// explicit-utility boundary. Direct execution always reaches 25006.
			var held *sql.Stmt
			if prepared {
				var err error
				held, err = c.PrepareContext(p.Ctx, command)
				if err != nil {
					t.Fatal("autocommit Prepare before read-only Execute", err)
				}
				defer held.Close()
			}
			run(fixture.Exec("BEGIN READ ONLY", 0))
			s := fixture.ImplicitError(command, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + strings.Fields(command)[0] + " in a read-only transaction"})
			var source fixture.Executor = c
			if held != nil {
				source = fixture.HeldStatement{Executor: c, Statement: held, SQL: command}
			}
			fixture.RunStep(t, p.Ctx, source, b.Recorder, s)
			run(fixture.Exec("ROLLBACK", 0))
		}
	})
}

func TestReferenceRoleGrantLifecycle(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		// Preparing an absent object/grantee does not resolve names or publish.
		q := "GRANT SELECT ON t TO rg_future"
		utility, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("effect-free utility Prepare", err)
		}
		defer utility.Close()
		run(fixture.Exec("CREATE ROLE rg_future", 0))
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_future", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: utility, SQL: q}, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_future", 0))
		selectQ := "SELECT a FROM t"
		stmt, err := c.PrepareContext(p.Ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: selectQ}
		want := fixture.Select(selectQ, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		old, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("REVOKE SELECT ON t FROM rg_future", 0))
		fixture.DeleteReferenceConsume(t, b.PG, old, nil, want)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleGrantDenied(selectQ, "t"))
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Rename/move retain ACL identity; a replacement column does not inherit it.
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
		run(fixture.Exec("ALTER TABLE t ADD COLUMN a INT DEFAULT 9", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT renamed FROM t", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE t DROP COLUMN renamed", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			return
		} // Native controls have no PostgreSQL API counterpart.
		var out bytes.Buffer
		err = stuntgres.Dump(p.Ctx, p.Native, &out)
		if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit relation or column ACL" || out.Len() != 0 {
			t.Fatal("explicit ACL Dump boundary", out.String(), err)
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT); GRANT SELECT ON replacement TO rg_reader"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"})
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_future", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		result, err := c.ExecContext(ctx, "GRANT SELECT ON t TO rg_reader")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled grant", result, err)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT rg_group TO rg_reader WITH INHERIT TRUE", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11)", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_future", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_group", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
