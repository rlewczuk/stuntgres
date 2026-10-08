//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"io"
	"net/url"
	"testing"
	"time"

	"stuntgres"
)

func TestReferenceSessionIdentityFormsAndFreshness(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_reference_role"})
	p := newReferencePair(t)
	for _, b := range []struct {
		name, bootstrap, database string
		db                        *sql.DB
		record                    *referenceRecorder
		native                    bool
	}{
		{"native", "postgres", "reference/" + t.Name(), p.memory, p.memoryRecord, true},
		{"postgres", "stuntgres", "stuntgres_reference", p.postgres, p.postgresRecord, false},
	} {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.prepared, s.query)
				referenceRunStep(t, p.ctx, conn, b.record, s)
			}
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT current_catalog=current_database(),current_role=current_user,session_user=user", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
				s.prepared = prepared
				run(s)
				s = referenceSelect("SELECT current_catalog,current_database(),current_role,current_user,session_user,user", []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}, [][]any{{b.database, b.database, b.bootstrap, b.bootstrap, b.bootstrap, b.bootstrap}})
				s.prepared = prepared
				run(s)
			}
			system := any(nil)
			if !b.native {
				system = "scram-sha-256:stuntgres"
			}
			run(referenceSelect("SELECT system_user", []string{"system_user"}, []string{"TEXT"}, [][]any{{system}}))
			conditional := system
			if conditional == nil {
				conditional = "trusted"
			}
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT CAST(COALESCE(system_user,'trusted') AS TEXT) AS identity_text", []string{"identity_text"}, []string{"TEXT"}, [][]any{{conditional}})
				s.prepared = prepared
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				for _, prefix := range []string{"pg_catalog.", `""`} {
					spell := func(name string) string {
						if prefix == `""` {
							return pq.QuoteIdentifier(name)
						}
						return prefix + name
					}
					query := "SELECT " + spell("current_database") + "()," + spell("current_user") + "()," + spell("session_user") + "()," + spell("system_user") + "()"
					s := referenceSelect(query, []string{"current_database", "current_user", "session_user", "system_user"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{b.database, b.bootstrap, b.bootstrap, system}})
					s.prepared = prepared
					run(s)
				}
			}
			run(referenceExec("CREATE ROLE identity_reference_role", 0))
			if !b.native {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_reference_role", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION identity_reference_role", 0))
			run(referenceExec("CREATE TABLE identity_reference_t(id INT,who TEXT DEFAULT current_user,session_name TEXT DEFAULT session_user)", 0))
			run(referenceExec("CREATE VIEW identity_reference_v AS SELECT current_user AS effective,system_user AS auth FROM identity_reference_t", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			held, err := conn.PrepareContext(p.ctx, "SELECT current_role,current_user,session_user,user")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			identity := func(want string) {
				t.Helper()
				var role, current, session, user string
				if err := held.QueryRowContext(p.ctx).Scan(&role, &current, &session, &user); err != nil || role != want || current != want || session != b.bootstrap || user != want {
					t.Fatal(role, current, session, user, err)
				}
			}
			identity(b.bootstrap)
			for _, q := range []string{"SET ROLE identity_reference_role", "SET SESSION ROLE 'identity_reference_role'", "SET ROLE TO identity_reference_role", "SET ROLE = identity_reference_role"} {
				run(referenceExec(q, 0))
				identity("identity_reference_role")
				run(referenceExec("SET ROLE NONE", 0))
				identity(b.bootstrap)
			}
			run(referenceExec("SET ROLE identity_reference_role", 0))
			run(referenceExec("SET ROLE TO DEFAULT", 0))
			identity(b.bootstrap)
			if b.native {
				run(referenceExec("BEGIN", 0))
				run(referenceImplicitError("SET ROLE identity_reference_role", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				run(referenceExec("ROLLBACK", 0))
				identity(b.bootstrap)
			} else {
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SET ROLE identity_reference_role", 0))
				identity("identity_reference_role")
				run(referenceExec("ROLLBACK", 0))
				identity(b.bootstrap)
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SET LOCAL ROLE identity_reference_role", 0))
				identity("identity_reference_role")
				run(referenceExec("COMMIT", 0))
				identity(b.bootstrap)
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, code, message, pos, hint string }{
					{"SELECT current_user()", "42601", `syntax error at or near "("`, "20", ""},
					{"SELECT current_catalog()", "42601", `syntax error at or near "("`, "23", ""},
					{"SELECT current_role()", "42601", `syntax error at or near "("`, "20", ""},
					{"SELECT session_user()", "42601", `syntax error at or near "("`, "20", ""},
					{"SELECT user()", "42601", `syntax error at or near "("`, "12", ""},
					{"SELECT system_user()", "42601", `syntax error at or near "("`, "19", ""},
					{"SELECT current_database(1)", "42883", "function current_database(integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT current_database(NULL)", "42883", "function current_database(unknown) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT current_database('x')", "42883", "function current_database(unknown) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT current_database(1::BIGINT)", "42883", "function current_database(bigint) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT current_database(1,2)", "42883", "function current_database(integer, integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT current_database(VARIADIC 1)", "42883", "function current_database(integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT pg_catalog.current_catalog()", "42883", "function pg_catalog.current_catalog() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT pg_catalog.user()", "42883", "function pg_catalog.user() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{`SELECT "CURRENT_DATABASE"()`, "42883", "function CURRENT_DATABASE() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT pg_catalog.current_role()", "42883", "function pg_catalog.current_role() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
					{"SET ROLE DEFAULT", "42601", `syntax error at or near "DEFAULT"`, "10", ""},
					{"SET SESSION ROLE DEFAULT", "42601", `syntax error at or near "DEFAULT"`, "18", ""},
					{`SELECT "current_user" FROM identity_reference_t`, "42703", `column "current_user" does not exist`, "8", ""},
					{"SELECT current_user+1 FROM identity_reference_t", "42883", "operator does not exist: name + integer", "20", "No operator matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT id FROM identity_reference_t WHERE current_user", "42804", "argument of WHERE must be type boolean, not type name", "43", ""},
					{"SELECT missing_identity_schema.current_database()", "3F000", `schema "missing_identity_schema" does not exist`, "8", ""},
					{"/* é */ SELECT current_database(NULL)", "42883", "function current_database(unknown) does not exist", "16", "No function matches the given name and argument types. You might need to add explicit type casts."},
				} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
					s.prepared = prepared
					run(s)
				}
			}
			run(referenceExec("SET ROLE identity_reference_role", 0))
			run(referenceExec("INSERT INTO identity_reference_t(id) VALUES(1)", 1))
			run(referenceSelect("SELECT who,session_name FROM identity_reference_t", []string{"who", "session_name"}, []string{"TEXT", "TEXT"}, [][]any{{"identity_reference_role", b.bootstrap}}))
			view, err := conn.PrepareContext(p.ctx, "SELECT effective,auth FROM identity_reference_v")
			if err != nil {
				t.Fatal(err)
			}
			viewIdentity := func(want string) {
				t.Helper()
				var effective string
				var auth any
				if err := view.QueryRowContext(p.ctx).Scan(&effective, &auth); err != nil || effective != want || auth != system {
					t.Fatal(effective, auth, err)
				}
			}
			viewIdentity("identity_reference_role")
			run(referenceExec("RESET ROLE", 0))
			viewIdentity(b.bootstrap)
			view.Close()
			if b.native {
				// DROP VIEW is not a native capability. Reset is the existing
				// public control for removing this view and its source table.
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			} else {
				run(referenceExec("DROP VIEW identity_reference_v", 0))
				run(referenceExec("DROP TABLE identity_reference_t", 0))
			}
			if !b.native {
				run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM identity_reference_role", 0))
			}
			run(referenceExec("DROP ROLE identity_reference_role", 0))
		})
	}
}

// The normal fixture deliberately uses SCRAM. A dedicated HBA rule and a
// password-free real pq connection prove trust semantics, not a fabricated NULL.
func TestReferenceSessionIdentityTrust(t *testing.T) {
	if err := referenceEnsureStarted(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const role = "identity_trust_witness"
	if _, err := referenceAdmin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if _, err := referenceAdmin.ExecContext(cleanup, "DROP ROLE "+role); err != nil {
			t.Error("trust role cleanup:", err)
		}
	})
	var hba string
	if err := referenceAdmin.QueryRowContext(ctx, "SHOW hba_file").Scan(&hba); err != nil {
		t.Fatal(err)
	}
	backup := hba + ".identity-backup"
	exec := func(ctx context.Context, script string) error {
		code, out, err := referenceContainer.Exec(ctx, []string{"sh", "-c", script})
		if err != nil {
			return err
		}
		body, err := io.ReadAll(out)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("container shell exit %d: %s", code, body)
		}
		return nil
	}
	// Acquire the backup before any live-file mutation and immediately own
	// restoration. Even a partial rewrite failure then runs this cleanup.
	if err := exec(ctx, fmt.Sprintf("cp %s %s", pq.QuoteLiteral(hba), pq.QuoteLiteral(backup))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		restoreErr := exec(cleanup, fmt.Sprintf("cp %s %s", pq.QuoteLiteral(backup), pq.QuoteLiteral(hba)))
		if restoreErr != nil {
			t.Error("trust HBA restoration:", restoreErr)
		}
		// Reload must be attempted even if file restoration failed.
		var reloaded bool
		reloadErr := referenceAdmin.QueryRowContext(cleanup, "SELECT pg_reload_conf()").Scan(&reloaded)
		if reloadErr == nil && !reloaded {
			reloadErr = fmt.Errorf("pg_reload_conf returned false")
		}
		if reloadErr != nil {
			t.Error("trust HBA reload:", reloadErr)
		}
		if restoreErr == nil && reloadErr == nil {
			if err := exec(cleanup, fmt.Sprintf("rm -f %s %s.identity-new", pq.QuoteLiteral(backup), pq.QuoteLiteral(hba))); err != nil {
				t.Error("trust temporary-file cleanup:", err)
			}
		}
	})
	if err := exec(ctx, fmt.Sprintf("{ printf 'host stuntgres_reference %s all trust\\n'; cat %s; } > %s.identity-new && cp %s.identity-new %s", role, pq.QuoteLiteral(backup), pq.QuoteLiteral(hba), pq.QuoteLiteral(hba), pq.QuoteLiteral(hba))); err != nil {
		t.Fatal(err)
	}
	var reloaded bool
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_reload_conf()").Scan(&reloaded); err != nil || !reloaded {
		t.Fatal("trust HBA reload:", reloaded, err)
	}
	// An explicit wrong password would fail SCRAM, proving this connection used trust.
	parsed, err := url.Parse(referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, "not-the-password")
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT session_user,system_user")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "NAME" || types[1].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	var session string
	var system any
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Scan(&session, &system); err != nil || session != role || system != nil {
		t.Fatal(session, system, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
}

func TestReferenceSessionIdentitySessionAuthorityAndMembership(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_member_a", "identity_member_b", "identity_member_c"})
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
		native bool
	}{{"native", p.memory, p.memoryRecord, true}, {"postgres", p.postgres, p.postgresRecord, false}} {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.prepared, s.query)
				referenceRunStep(t, p.ctx, conn, b.record, s)
			}
			for _, q := range []string{"CREATE USER identity_member_a", "CREATE ROLE identity_member_b", "CREATE ROLE identity_member_c", "CREATE SCHEMA identity_hidden"} {
				run(referenceExec(q, 0))
			}
			if !b.native {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_member_a", 0))
			}
			run(referenceExec("SET ROLE identity_member_a", 0))
			run(referenceExec("SET ROLE identity_member_b", 0))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("SET SESSION AUTHORIZATION identity_member_a", 0))
			run(referenceExec("SET ROLE identity_member_a", 0))
			run(referenceExec("CREATE TABLE identity_child(id INT)", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SET ROLE identity_member_b", referenceDiagnostic{Code: "42501", Message: `permission denied to set role "identity_member_b"`})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				s = referenceImplicitError("SET ROLE absent_identity_role", referenceDiagnostic{Code: "22023", Message: `role "absent_identity_role" does not exist`})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				s = referenceImplicitError("SELECT identity_hidden.current_database()", referenceDiagnostic{Code: "42501", Message: "permission denied for schema identity_hidden", Position: "8"})
				s.prepared = prepared
				run(s)
				for _, tc := range []struct{ query, position string }{
					{"SELECT missing_identity_schema.current_database(missing_column) FROM identity_child", "49"},
					{"SELECT identity_hidden.current_database(missing_column) FROM identity_child", "41"},
				} {
					s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: tc.position})
					s.prepared = prepared
					run(s)
				}
			}
			run(referenceSelect("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_a", "identity_member_a"}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			if !b.native {
				run(referenceExec("GRANT identity_member_b TO identity_member_a WITH SET TRUE, INHERIT FALSE", 0))
				run(referenceExec("GRANT identity_member_c TO identity_member_b WITH SET TRUE, INHERIT FALSE", 0))
				run(referenceExec("SET SESSION AUTHORIZATION identity_member_a", 0))
				run(referenceExec("SET ROLE identity_member_c", 0))
				run(referenceSelect("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_c", "identity_member_a"}}))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				// Break the second SET edge while enabling both other options.
				// Neither inherited privileges nor ADMIN authorize SET ROLE.
				run(referenceExec("GRANT identity_member_c TO identity_member_b WITH SET FALSE, INHERIT TRUE, ADMIN TRUE", 0))
				run(referenceExec("SET SESSION AUTHORIZATION identity_member_a", 0))
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("SET ROLE identity_member_c", referenceDiagnostic{Code: "42501", Message: `permission denied to set role "identity_member_c"`})
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
					run(referenceSelect("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_a", "identity_member_a"}}))
				}
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM identity_member_a", 0))
			}
			run(referenceExec("DROP TABLE identity_child", 0))
			run(referenceExec("DROP SCHEMA identity_hidden", 0))
			run(referenceExec("DROP ROLE identity_member_a,identity_member_b,identity_member_c", 0))
		})
	}
}

func TestReferenceSessionIdentityRoleSentinels(t *testing.T) {
	roleReferenceCleanup(t, []string{"default", "NONE", "identity_sentinel_selected"})
	p := newReferencePair(t)
	for _, b := range []struct {
		name, bootstrap string
		db              *sql.DB
		record          *referenceRecorder
	}{
		{"native", "postgres", p.memory, p.memoryRecord},
		{"postgres", "stuntgres", p.postgres, p.postgresRecord},
	} {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.prepared, s.query)
				referenceRunStep(t, p.ctx, conn, b.record, s)
			}
			for _, q := range []string{`CREATE ROLE "default"`, `CREATE ROLE "NONE"`, "CREATE ROLE identity_sentinel_selected"} {
				run(referenceExec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				selectRole := func(query, want string) {
					t.Helper()
					s := referenceExec(query, 0)
					s.prepared = prepared
					run(s)
					run(referenceSelect("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{want, b.bootstrap}}))
				}
				for _, reset := range []string{"SET ROLE TO DEFAULT", "SET ROLE = DEFAULT", `SET ROLE "none"`, "SET ROLE 'none'"} {
					selectRole("SET ROLE identity_sentinel_selected", "identity_sentinel_selected")
					selectRole(reset, b.bootstrap)
				}
				for _, query := range []string{`SET ROLE "default"`, "SET ROLE 'default'"} {
					selectRole(query, "default")
				}
				for _, query := range []string{`SET ROLE "NONE"`, "SET ROLE 'NONE'"} {
					selectRole(query, "NONE")
				}
				selectRole("RESET ROLE", b.bootstrap)
			}
			run(referenceExec(`DROP ROLE "default","NONE",identity_sentinel_selected`, 0))
		})
	}
}
