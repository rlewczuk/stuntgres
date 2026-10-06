//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceUpdateCoreDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b INT,n SMALLINT NOT NULL)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(1,2,1),(40000,3,1)", 2))
		}
		for _, tc := range []struct {
			q string
			d referenceDiagnostic
		}{
			{"UPDATE missing SET a=1", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "8"}},
			{"UPDATE /*é*/ missing SET a=1", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "14"}},
			{"UPDATE t SET missing=1", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "14"}},
			{"UPDATE t SET a=missing", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "16"}},
			{"UPDATE t SET a=TRUE", referenceDiagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Position: "16", Hint: "You will need to rewrite or cast the expression."}},
			{"UPDATE t SET a='bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "16"}},
			{"UPDATE t SET a=1,a=2", referenceDiagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
			{"UPDATE t SET ctid=1", referenceDiagnostic{Code: "0A000", Message: `cannot assign to system column "ctid"`, Position: "14"}},
			{"UPDATE t SET a=1 WHERE 1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
			{"UPDATE t SET a=1 WHERE", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
			{"UPDATE t SET missing=1 WHERE 1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "30"}},
			{"UPDATE t SET a=TRUE,b=missing", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
		} {
			p.step(t, referenceImplicitError(tc.q, tc.d))
			step := referencePrepared(referenceImplicitError(tc.q, tc.d))
			step.errorStage = "prepare"
			p.step(t, step)
		}
	}
	p.step(t, referencePrepared(referenceImplicitError("UPDATE t SET n=$1 WHERE b=2", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, 2, null).", Schema: "public", Table: "t", Column: "n"}, nil)))
	p.step(t, referenceSelect("SELECT a,b,n FROM t ORDER BY b", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT2"}, [][]any{{int64(1), int64(2), int64(1)}, {int64(40000), int64(3), int64(1)}}))
}

func TestReferenceUpdateCoreColumnPrivileges(t *testing.T) {
	p := newReferencePair(t)
	memory, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	postgres, err := p.postgres.Conn(p.ctx)
	if err != nil {
		memory.Close()
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, conn := range []*sql.Conn{memory, postgres} {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP TABLE IF EXISTS t", "DROP ROLE update_acl_role"} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Error("privilege fixture cleanup", q, err)
				}
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	native := func(s referenceStep) { referenceRunStep(t, p.ctx, memory, p.memoryRecord, s) }
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, postgres, p.postgresRecord, s) }
	// Explicit schema recreation has no default PostgreSQL PUBLIC USAGE.
	// Match the native namespace baseline without granting any table access.
	run(referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
	// Only the shared PostgreSQL fixture can retain a role from a failed case;
	// the native pair owns a fresh Driver and lacks DROP ROLE IF EXISTS.
	run(referenceExec("DROP ROLE IF EXISTS update_acl_role", 0))
	for _, execute := range []func(referenceStep){native, run} {
		execute(referenceExec("CREATE ROLE update_acl_role", 0))
		execute(referenceExec("CREATE TABLE t(a INT,b INT,c INT)", 0))
		execute(referenceExec("INSERT INTO t VALUES(1,2,3)", 1))
		execute(referenceExec("SET SESSION AUTHORIZATION update_acl_role", 0))
		execute(referenceImplicitError("UPDATE t SET a=7", referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}))
		execute(referenceExec("RESET SESSION AUTHORIZATION", 0))
	}
	run(referenceExec("GRANT UPDATE(a) ON t TO update_acl_role", 0))
	run(referenceExec("SET SESSION AUTHORIZATION update_acl_role", 0))
	run(referenceExec("UPDATE t SET a=7", 1))
	for _, q := range []string{"UPDATE t SET a=b", "UPDATE t SET a=8 WHERE c=3", "UPDATE t SET b=8"} {
		run(referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}))
	}
	run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	run(referenceExec("GRANT SELECT(a,b) ON t TO update_acl_role", 0))
	run(referenceExec("SET SESSION AUTHORIZATION update_acl_role", 0))
	run(referenceExec("UPDATE t SET a=b WHERE a=7", 1))
	run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	run(referenceSelect("SELECT a,b,c FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(2), int64(3)}}))
}
