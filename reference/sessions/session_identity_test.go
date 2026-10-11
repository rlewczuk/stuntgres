//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"io"
	"net/url"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// The normal fixture deliberately uses SCRAM. A dedicated HBA rule and a
// password-free real pq connection prove trust semantics, not a fabricated NULL.
func TestReferenceSessionIdentityTrust(t *testing.T) {
	if err := fixture.EnsureStarted(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const role = "identity_trust_witness"
	if _, err := fixture.Admin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if _, err := fixture.Admin.ExecContext(cleanup, "DROP ROLE "+role); err != nil {
			t.Error("trust role cleanup:", err)
		}
	})
	var hba string
	if err := fixture.Admin.QueryRowContext(ctx, "SHOW hba_file").Scan(&hba); err != nil {
		t.Fatal(err)
	}
	backup := hba + ".identity-backup"
	exec := func(ctx context.Context, script string) error {
		code, out, err := fixture.Container.Exec(ctx, []string{"sh", "-c", script})
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
		reloadErr := fixture.Admin.QueryRowContext(cleanup, "SELECT pg_reload_conf()").Scan(&reloaded)
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
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_reload_conf()").Scan(&reloaded); err != nil || !reloaded {
		t.Fatal("trust HBA reload:", reloaded, err)
	}
	// An explicit wrong password would fail SCRAM, proving this connection used trust.
	parsed, err := url.Parse(fixture.DSN)
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
	fixture.RoleReferenceCleanup(t, []string{"identity_member_a", "identity_member_b", "identity_member_c"})
	p := fixture.NewPair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *fixture.Recorder
		native bool
	}{{"postgres", p.Postgres, p.PostgresRecord, false}} {
		t.Run(b.name, func(t *testing.T) {
			t.Cleanup(func() {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS identity_hidden CASCADE"); err != nil {
					t.Error("identity membership schema cleanup", err)
				}
			})
			conn, err := b.db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.Prepared, s.Query)
				fixture.RunStep(t, p.Ctx, conn, b.record, s)
			}
			for _, q := range []string{"CREATE USER identity_member_a", "CREATE ROLE identity_member_b", "CREATE ROLE identity_member_c", "CREATE SCHEMA identity_hidden"} {
				run(fixture.Exec(q, 0))
			}
			if !b.native {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO identity_member_a", 0))
			}
			run(fixture.Exec("SET ROLE identity_member_a", 0))
			run(fixture.Exec("SET ROLE identity_member_b", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION identity_member_a", 0))
			run(fixture.Exec("SET ROLE identity_member_a", 0))
			run(fixture.Exec("CREATE TABLE identity_child(id INT)", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SET ROLE identity_member_b", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "identity_member_b"`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				s = fixture.ImplicitError("SET ROLE absent_identity_role", fixture.Diagnostic{Code: "22023", Message: `role "absent_identity_role" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				s = fixture.ImplicitError("SELECT identity_hidden.current_database()", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema identity_hidden", Position: "8"})
				s.Prepared = prepared
				run(s)
				for _, tc := range []struct{ query, position string }{
					{"SELECT missing_identity_schema.current_database(missing_column) FROM identity_child", "49"},
					{"SELECT identity_hidden.current_database(missing_column) FROM identity_child", "41"},
				} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: tc.position})
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_a", "identity_member_a"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if !b.native {
				run(fixture.Exec("GRANT identity_member_b TO identity_member_a WITH SET TRUE, INHERIT FALSE", 0))
				run(fixture.Exec("GRANT identity_member_c TO identity_member_b WITH SET TRUE, INHERIT FALSE", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION identity_member_a", 0))
				run(fixture.Exec("SET ROLE identity_member_c", 0))
				run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_c", "identity_member_a"}}))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				// Break the second SET edge while enabling both other options.
				// Neither inherited privileges nor ADMIN authorize SET ROLE.
				run(fixture.Exec("GRANT identity_member_c TO identity_member_b WITH SET FALSE, INHERIT TRUE, ADMIN TRUE", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION identity_member_a", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SET ROLE identity_member_c", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "identity_member_c"`})
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"identity_member_a", "identity_member_a"}}))
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM identity_member_a", 0))
			}
			run(fixture.Exec("DROP TABLE identity_child", 0))
			run(fixture.Exec("DROP SCHEMA identity_hidden", 0))
			run(fixture.Exec("DROP ROLE identity_member_a,identity_member_b,identity_member_c", 0))
		})
	}
}
