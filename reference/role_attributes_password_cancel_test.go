package reference_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestReferenceRoleAttributesSelfPasswordCancellation(t *testing.T) {
	// Valid SCRAM syntax with a 16-byte salt and two 32-byte keys. These are
	// deliberately fixed encoded keys, not two billion iterations of test setup.
	// REL_18_0 checks empty-password derivation even for a preencoded verifier.
	salt := base64.StdEncoding.EncodeToString([]byte("cancel-salt-1234"))
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	verifier := "SCRAM-SHA-256$2147483647:" + salt + "$" + key + ":" + key
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"rac_password"})
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				roleAttributesRun(t, p, b, b.db, prepared, referenceExec("CREATE ROLE rac_password LOGIN CONNECTION LIMIT 1 PASSWORD 'role-attributes-fixture'", 0))
				var before string
				readVerifier := func() string {
					var got string
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rac_password'").Scan(&got); err != nil {
						t.Fatal(err)
					}
					return got
				}
				if b.pg {
					before = readVerifier()
					roleAttributesCheckSCRAM(t, before, "role-attributes-fixture")
				}
				db := roleAttributesPool(t, p, b, "rac_password")
				db.SetMaxOpenConns(1)
				db.SetMaxIdleConns(0) // Conn.Close below must physically tear down.
				c, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(step referenceStep) { roleAttributesRun(t, p, b, c, prepared, step) }
				run(referenceSelect("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"rac_password", "rac_password"}}))
				query := "ALTER ROLE CURRENT_USER PASSWORD " + pq.QuoteLiteral(verifier)
				var stmt *sql.Stmt
				if prepared {
					stmt, err = c.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal("password cancellation must reach Execute, not fail Prepare:", err)
					}
					defer stmt.Close()
				}
				budget := 40 * time.Millisecond
				if b.pg {
					run(referenceExec("SET statement_timeout='40ms'", 0))
					budget = 5 * time.Second // watchdog, not the PostgreSQL cancel trigger
				}
				ctx, cancel := context.WithTimeout(p.ctx, budget)
				started := time.Now()
				var result sql.Result
				if stmt != nil {
					result, err = stmt.ExecContext(ctx)
				} else {
					result, err = c.ExecContext(ctx, query)
				}
				elapsed := time.Since(started)
				cancel()
				if result != nil {
					t.Fatal("canceled password alteration exposed a result")
				}
				if elapsed > 2*time.Second {
					t.Fatalf("password derivation cancellation took %s, want at most 2s", elapsed)
				}
				if b.pg {
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"})
					run(referenceExec("RESET statement_timeout", 0))
					if got := readVerifier(); got != before {
						t.Fatalf("canceled PASSWORD changed stored verifier: %q -> %q", before, got)
					}
				} else if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("native password derivation lost operation deadline: %v", err)
				}
				if stmt != nil {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
				// Same physical owner remains healthy, including the changed route.
				run(referenceSelect("SELECT 7 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(referenceExec("ALTER ROLE CURRENT_USER PASSWORD 'role-attributes-fixture'", 0))
				closed := time.Now()
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				if time.Since(closed) > 2*time.Second {
					t.Fatal("physical teardown retained canceled password work")
				}
				// Limit one proves teardown releases the original physical slot;
				// PostgreSQL also authenticates against the unchanged/reset password.
				replacement, err := roleAttributesPhysical(t, p, b, "rac_password")
				if err != nil {
					t.Fatal("post-cancellation physical replacement:", err)
				}
				if err := replacement.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
