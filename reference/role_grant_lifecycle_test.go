package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceRoleGrantTransactions(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		q := "GRANT SELECT ON t TO rg_reader"
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			run(referenceExec("BEGIN", 0))
			if b.pg {
				run(referenceExec(q, 0))
				run(referenceExec(end, 0))
			} else {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
				// The unsupported utility fails the ordinary native transaction.
				s = referenceImplicitError("SELECT a FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				s.selectRows = true
				run(s)
				run(referenceExec("ROLLBACK", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
			if b.pg && end == "COMMIT" {
				run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			} else {
				run(roleGrantDenied("SELECT a FROM t", "t"))
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, command := range []string{"GRANT SELECT ON missing TO missing_role", "REVOKE SELECT ON missing FROM missing_role"} {
			// Execute read-only precedence is separate from the native Prepare
			// explicit-utility boundary. Direct execution always reaches 25006.
			var held *sql.Stmt
			if prepared {
				var err error
				held, err = c.PrepareContext(p.ctx, command)
				if err != nil {
					t.Fatal("autocommit Prepare before read-only Execute", err)
				}
				defer held.Close()
			}
			run(referenceExec("BEGIN READ ONLY", 0))
			s := referenceImplicitError(command, referenceDiagnostic{Code: "25006", Message: "cannot execute " + strings.Fields(command)[0] + " in a read-only transaction"})
			var source referenceExecutor = c
			if held != nil {
				source = referenceHeldStatement{referenceExecutor: c, statement: held, query: command}
			}
			referenceRunStep(t, p.ctx, source, b.recorder, s)
			run(referenceExec("ROLLBACK", 0))
		}
	})
}

func TestReferenceRoleGrantLifecycle(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		// Preparing an absent object/grantee does not resolve names or publish.
		q := "GRANT SELECT ON t TO rg_future"
		utility, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal("effect-free utility Prepare", err)
		}
		defer utility.Close()
		run(referenceExec("CREATE ROLE rg_future", 0))
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("SET SESSION AUTHORIZATION rg_future", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: utility, query: q}, b.recorder, referenceExec(q, 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_future", 0))
		selectQ := "SELECT a FROM t"
		stmt, err := c.PrepareContext(p.ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: selectQ}
		want := referenceSelect(selectQ, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		referenceRunStep(t, p.ctx, held, b.recorder, want)
		admin, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		old, err := stmt.QueryContext(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		referenceRunStep(t, p.ctx, admin, b.recorder, referenceExec("REVOKE SELECT ON t FROM rg_future", 0))
		deleteReferenceConsume(t, b.pg, old, nil, want)
		referenceRunStep(t, p.ctx, held, b.recorder, roleGrantDenied(selectQ, "t"))
		referenceRunStep(t, p.ctx, admin, b.recorder, referenceExec(q, 0))
		referenceRunStep(t, p.ctx, held, b.recorder, want)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Rename/move retain ACL identity; a replacement column does not inherit it.
		run(referenceExec("GRANT SELECT(a) ON t TO rg_reader", 0))
		run(referenceExec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
		run(referenceExec("ALTER TABLE t ADD COLUMN a INT DEFAULT 9", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT renamed FROM t", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("ALTER TABLE t DROP COLUMN renamed", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		if b.pg {
			return
		} // Native controls have no PostgreSQL API counterpart.
		var out bytes.Buffer
		err = stuntgres.Dump(p.ctx, p.native, &out)
		if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit relation or column ACL" || out.Len() != 0 {
			t.Fatal("explicit ACL Dump boundary", out.String(), err)
		}
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(a INT); GRANT SELECT ON replacement TO rg_reader"))
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"})
		run(referenceExec("SET SESSION AUTHORIZATION rg_future", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		result, err := c.ExecContext(ctx, "GRANT SELECT ON t TO rg_reader")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled grant", result, err)
		}
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT rg_group TO rg_reader WITH INHERIT TRUE", 0))
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(11)", 1))
		run(referenceExec("SET SESSION AUTHORIZATION rg_future", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_group", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}
