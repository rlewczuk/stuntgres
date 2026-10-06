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

// Native SQL GRANT remains unsupported. The paired native mechanism is the
// engine's real catalog-ACL/Execute/Finish regression; this is the public-only
// PostgreSQL oracle, with independent complete diagnostics, not secret absence.
func TestReferenceUpdateCoreNotNullDetailColumnAuthority(t *testing.T) {
	for _, mode := range []string{"update_only", "select_readable", "all_column_select", "table_select", "modified_readable", "owner", "superuser"} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					p := newReferencePair(t)
					ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
					defer cancel()
					conn, err := p.postgres.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer func() {
						cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP TABLE IF EXISTS t", "DROP ROLE IF EXISTS nn_acl_actor", "DROP ROLE IF EXISTS nn_acl_owner"} {
							if _, err := conn.ExecContext(cleanup, q); err != nil {
								t.Error("column detail fixture cleanup", q, err)
							}
						}
					}()
					run := func(step referenceStep) { referenceRunStep(t, ctx, conn, p.postgresRecord, step) }
					for _, q := range []string{
						"DROP ROLE IF EXISTS nn_acl_actor", "DROP ROLE IF EXISTS nn_acl_owner",
						"CREATE ROLE nn_acl_owner LOGIN", "CREATE ROLE nn_acl_actor LOGIN",
						"GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC",
						"SET SESSION AUTHORIZATION nn_acl_owner",
						"CREATE TABLE t(n INT NOT NULL,secret TEXT,readable TEXT)",
						"INSERT INTO t VALUES(1,'private','visible')",
						"RESET SESSION AUTHORIZATION",
						"GRANT UPDATE(n) ON t TO nn_acl_actor",
					} {
						n := int64(0)
						if q == "INSERT INTO t VALUES(1,'private','visible')" {
							n = 1
						}
						run(referenceExec(q, n))
					}
					detail := "Failing row contains (n) = (null)."
					switch mode {
					case "select_readable":
						run(referenceExec("GRANT SELECT(readable) ON t TO nn_acl_actor", 0))
						detail = "Failing row contains (n, readable) = (null, visible)."
					case "all_column_select":
						run(referenceExec("GRANT SELECT(n,secret,readable) ON t TO nn_acl_actor", 0))
						detail = "Failing row contains (n, secret, readable) = (null, private, visible)."
					case "table_select":
						run(referenceExec("GRANT SELECT ON t TO nn_acl_actor", 0))
						detail = "Failing row contains (null, private, visible)."
					case "modified_readable":
						run(referenceExec("GRANT UPDATE(readable) ON t TO nn_acl_actor", 0))
						detail = "Failing row contains (n, readable) = (null, new)."
					case "owner", "superuser":
						detail = "Failing row contains (null, private, visible)."
					}
					actor := "nn_acl_actor"
					if mode == "owner" {
						actor = "nn_acl_owner"
					}
					if mode != "superuser" {
						run(referenceExec("SET SESSION AUTHORIZATION "+actor, 0))
					}
					query := "UPDATE t SET n=NULL"
					if prepared {
						query = "UPDATE t SET n=$1"
					}
					if mode == "modified_readable" {
						if prepared {
							query = "UPDATE t SET readable='new',n=$1"
						} else {
							query = "UPDATE t SET readable='new',n=NULL"
						}
					}
					var stmt *sql.Stmt
					if prepared {
						stmt, err = conn.PrepareContext(ctx, query)
						if err != nil {
							t.Fatal("NOT NULL assignment must prepare", err)
						}
						defer stmt.Close()
					}
					repeats := 1
					if prepared {
						repeats = 2
					}
					for range repeats {
						if explicit {
							run(referenceExec("BEGIN", 0))
							run(referenceExec("UPDATE t SET n=2", 1))
						}
						var result sql.Result
						if prepared {
							result, err = stmt.ExecContext(ctx, nil)
						} else {
							result, err = conn.ExecContext(ctx, query)
						}
						if result != nil {
							t.Fatal("failed UPDATE returned result", result)
						}
						referenceAssertDiagnostic(t, err, &referenceDiagnostic{
							Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
							Message: `null value in column "n" of relation "t" violates not-null constraint`,
							Detail:  detail, Schema: "public", Table: "t", Column: "n",
						})
						if explicit {
							run(referenceImplicitError("UPDATE t SET n=3", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
							run(referenceExec("ROLLBACK", 0))
						}
						run(referenceExec("RESET SESSION AUTHORIZATION", 0))
						run(referenceSelect("SELECT n,secret,readable FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "private", "visible"}}))
						if mode != "superuser" {
							run(referenceExec("SET SESSION AUTHORIZATION "+actor, 0))
						}
					}
				})
			}
		}
	}
}

// Independently specified PostgreSQL-only contextual assignment goldens. The
// positions are literal character offsets, not native AST/provider output.
func TestReferenceUpdateCoreArithmeticAssignmentDiagnostic(t *testing.T) {
	p := newReferencePair(t)
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, conn, p.postgresRecord, step) }
	run(referenceExec("CREATE TABLE t(f BOOL)", 0))
	for _, populated := range []bool{false, true} {
		wantRows := [][]any{}
		if populated {
			run(referenceExec("INSERT INTO t VALUES(TRUE),(FALSE)", 2))
			wantRows = [][]any{{false}, {true}}
		}
		for _, tc := range []struct {
			name, q, position, typ string
		}{
			{"plain", "UPDATE t SET f=1+2", "16", "integer"},
			{"grouped", "UPDATE t SET f=(1+2)", "17", "integer"},
			{"utf8_grouped", "/*é*/ UPDATE t SET f=(1+2)", "23", "integer"},
			{"postfix", "UPDATE t SET f=(1+2)::bigint", "17", "bigint"},
		} {
			for _, protocol := range []string{"exec", "query", "prepare"} {
				t.Run(map[bool]string{false: "empty", true: "populated"}[populated]+"/"+tc.name+"/"+protocol, func(t *testing.T) {
					defer run(referenceSelect("SELECT f FROM t ORDER BY f", []string{"f"}, []string{"BOOL"}, wantRows))
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "f" is of type boolean but expression is of type ` + tc.typ, Hint: "You will need to rewrite or cast the expression.", Position: tc.position}
					switch protocol {
					case "exec":
						result, err := conn.ExecContext(p.ctx, tc.q)
						if result != nil {
							t.Fatal("invalid assignment returned a result")
						}
						referenceAssertDiagnostic(t, err, &want)
					case "query":
						rows, err := conn.QueryContext(p.ctx, tc.q)
						if rows != nil {
							rows.Close()
							t.Fatal("invalid assignment exposed rows")
						}
						referenceAssertDiagnostic(t, err, &want)
					case "prepare":
						stmt, err := conn.PrepareContext(p.ctx, tc.q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid assignment prepared")
						}
						referenceAssertDiagnostic(t, err, &want)
					}
				})
			}
		}
	}
}
