//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCheckVolatileEffects(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s START 1", 0))
		run(referenceExec("CREATE TABLE t(a INT CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		run(referenceSelect("SELECT last_value FROM s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceExec("CREATE TABLE u(a INT CHECK(10/a>0))", 0))
		run(referenceExec("INSERT INTO u VALUES(1)", 1))
		run(referenceImplicitError("INSERT INTO u VALUES(2),(0)", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
		run(referenceSelect("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
func TestReferenceCheckNames(t *testing.T) {
	for _, tc := range []struct{ ddl, name, values string }{
		{"CREATE TABLE t(a INT CONSTRAINT z CHECK(FALSE),CONSTRAINT a CHECK(FALSE))", "a", "1"},
		{"CREATE TABLE t(a INT,b INT CHECK(a+a>0))", "t_a_check", "0, 1"},
		{"CREATE TABLE t(a INT,b INT,CHECK(a<b))", "t_check", "2, 1"},
		{"CREATE TABLE t(a INT CONSTRAINT t_a_check CHECK(a>0),CHECK(a>1))", "t_a_check1", "1"},
		{"CREATE TABLE t(a INT CHECK(a>0),CHECK(a>1),CHECK(a>2))", "t_a_check2", "2"},
	} {
		t.Run(tc.ddl, func(t *testing.T) {
			referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
				run(referenceExec(tc.ddl, 0))
				run(referenceImplicitError("INSERT INTO t VALUES("+tc.values+")", referenceCheckViolation("t", tc.name, tc.values)))
			})
		})
	}
}
func TestReferenceCheckPrivilegeDetailPostgres(t *testing.T) {
	for _, mode := range []string{"none", "readable", "columns", "table"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
			run(referenceExec("CREATE ROLE check_actor", 0))
			t.Cleanup(func() {
				if _, err := referenceAdmin.ExecContext(context.Background(), "DROP OWNED BY check_actor; DROP ROLE check_actor"); err != nil {
					t.Error(err)
				}
			})
			run(referenceExec("CREATE TABLE t(n INT,secret TEXT,readable TEXT,CONSTRAINT c CHECK(n=1 AND secret='private'))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'private','visible')", 1))
			run(referenceExec("GRANT UPDATE(n) ON t TO check_actor", 0))
			run(referenceExec("GRANT USAGE ON SCHEMA public TO check_actor", 0))
			detail := "n) = (2"
			switch mode {
			case "readable":
				run(referenceExec("GRANT SELECT(readable) ON t TO check_actor", 0))
				detail = "n, readable) = (2, visible"
			case "columns":
				run(referenceExec("GRANT SELECT(n,secret,readable) ON t TO check_actor", 0))
				detail = "n, secret, readable) = (2, private, visible"
			case "table":
				run(referenceExec("GRANT SELECT ON t TO check_actor", 0))
				detail = "2, private, visible"
			}
			run(referenceExec("SET ROLE check_actor", 0))
			run(referenceImplicitError("UPDATE t SET n=2", referenceCheckViolation("t", "c", detail)))
			run(referenceExec("RESET ROLE", 0))
			run(referenceSelect("SELECT * FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "private", "visible"}}))
		})
	}
}
func TestReferenceCheckTableoidBoundary(t *testing.T) {
	p := newReferencePair(t)
	t.Run("postgres_supported", func(t *testing.T) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t(a INT CHECK(tableoid>0))", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})} {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		}
	})
	t.Run("native_existing_system_column_boundary", func(t *testing.T) {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE t(a INT)", 0))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceImplicitError("SELECT tableoid FROM t", referenceDiagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "8"}))
	})
}
func TestReferenceCheckControls(t *testing.T) {
	p := newReferencePair(t)
	// Observe the SQL witness on PostgreSQL before exercising native controls.
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	run(referenceExec("CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1),(NULL)", 2))
	run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
	if _, err := p.native.ExecContext(p.ctx, "CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t VALUES(1),(NULL)"); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CONSTRAINT "c" CHECK`) || !strings.Contains(dump.String(), "NO INHERIT") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}}))
	p.step(t, referenceImplicitError("UPDATE t SET a=2", referenceCheckViolation("t", "c", "2")))
}
