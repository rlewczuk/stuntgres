package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceRoleExcludedCommands(t *testing.T) {
	roleReferenceCleanup(t, []string{"owner", "reader"})
	p := newReferenceScenario(t)
	if p.postgres != nil {
		if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			for _, q := range []string{"CREATE USER owner", "CREATE ROLE reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE sentinel(id INT)", "CREATE TABLE role(role INT)", "SET application_name TO 'SET ROLE reader'"} {
				roleScenarioCommand(t, c, q, 0, false)
			}
			roleScenarioCommand(t, c, "INSERT INTO sentinel VALUES(7)", 1, false)
			roleScenarioCommand(t, c, "INSERT INTO role(role) VALUES(8)", 1, true)
			roleScenarioCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
			roleScenarioCommand(t, peer, "SET application_name TO peer", 0, true)
			unchanged := func() {
				t.Helper()
				roleScenarioIdentity(t, c, "owner", nil, backend.pg)
				roleScenarioIdentity(t, peer, "reader", nil, backend.pg)
				for _, tc := range []struct {
					c    *sql.Conn
					want string
				}{{c, "SET ROLE reader"}, {peer, "peer"}} {
					var got string
					if err := tc.c.QueryRowContext(p.ctx, "SHOW application_name").Scan(&got); err != nil || got != tc.want {
						t.Fatal(got, err)
					}
				}
				for _, tc := range []struct {
					q    string
					want int64
				}{{"SELECT id FROM sentinel", 7}, {"SELECT role FROM role WHERE role=8 ORDER BY role", 8}} {
					referenceRunStep(t, p.ctx, c, backend.recorder, referenceSelect(tc.q, []string{map[int64]string{7: "id", 8: "role"}[tc.want]}, []string{"INT4"}, [][]any{{tc.want}}))
				}
			}
			unchanged()
			for _, tc := range []struct{ query, message, position string }{
				{"SET LOCAL ROLE reader", "SET LOCAL ROLE is unsupported", "11"},
				{"SET LOCAL ROLE 'reader'", "SET LOCAL ROLE is unsupported", "11"},
				{"SET LOCAL ROLE TO DEFAULT", "SET LOCAL ROLE is unsupported", "11"},
				{"SET LOCAL ROLE = NONE", "SET LOCAL ROLE is unsupported", "11"},
				{"INSERT INTO sentinel VALUES(9); SET LOCAL ROLE reader", "SET LOCAL ROLE is unsupported", "43"},
			} {
				for _, prepared := range []bool{false, true} {
					if backend.pg {
						// Actual PostgreSQL implements LOCAL ROLE. Under authenticated owner,
						// switching to unrelated reader fails rather than granting membership.
						if prepared && strings.Contains(tc.query, ";") {
							want := controlsReferenceDiagnostic("42601", "cannot insert multiple commands into a prepared statement")
							s := referenceErrorDiagnostic(tc.query, want)
							s.prepared = true
							s.errorStage = "prepare"
							referenceRunStep(t, p.ctx, c, backend.recorder, s)
						} else {
							code := "42501"
							if strings.Contains(tc.query, "DEFAULT") || strings.Contains(tc.query, "NONE") {
								code = ""
							}
							s := referenceExec(tc.query, 0)
							if code != "" {
								want := controlsReferenceDiagnostic(code, `permission denied to set role "reader"`)
								s = referenceErrorDiagnostic(tc.query, want)
							}
							s.prepared = prepared
							referenceRunStep(t, p.ctx, c, backend.recorder, s)
						}
					} else {
						want := controlsReferenceDiagnostic("0A000", tc.message)
						want.Position = tc.position
						s := referenceErrorDiagnostic(tc.query, want)
						s.prepared = prepared
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, backend.recorder, s)
					}
					unchanged()
				}
			}
			roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, true)
			bootstrap := "postgres"
			if backend.pg {
				bootstrap = "stuntgres"
			}
			roleScenarioIdentity(t, c, bootstrap, nil, backend.pg)
			roleScenarioIdentity(t, peer, "reader", nil, backend.pg)
			roleScenarioCommand(t, peer, "RESET SESSION AUTHORIZATION", 0, false)
		})
	}
}

func TestReferenceRoleDropConditionalBoundary(t *testing.T) {
	roleReferenceCleanup(t, []string{"reader", "leaked", "if", "exists", "IF", "EXISTS", "Reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			bootstrap := "postgres"
			if backend.pg {
				bootstrap = "stuntgres"
			}
			for _, q := range []string{"CREATE ROLE reader", "CREATE TABLE sentinel(id INT)", "SET application_name TO retained"} {
				roleScenarioCommand(t, c, q, 0, false)
			}
			roleScenarioCommand(t, c, "INSERT INTO sentinel VALUES(7)", 1, false)
			roleScenarioCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
			roleScenarioCommand(t, peer, "SET application_name TO peer", 0, false)
			unchanged := func() {
				t.Helper()
				roleScenarioIdentity(t, c, bootstrap, nil, backend.pg)
				roleScenarioIdentity(t, peer, "reader", nil, backend.pg)
				var value int64
				if err := c.QueryRowContext(p.ctx, "SELECT id FROM sentinel").Scan(&value); err != nil || value != 7 {
					t.Fatal(value, err)
				}
				for _, tc := range []struct {
					c    *sql.Conn
					want string
				}{{c, "retained"}, {peer, "peer"}} {
					var got string
					if err := tc.c.QueryRowContext(p.ctx, "SHOW application_name").Scan(&got); err != nil || got != tc.want {
						t.Fatal(got, err)
					}
				}
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION reader", 0, false)
				roleScenarioIdentity(t, c, "reader", nil, backend.pg)
				roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
			}
			for _, tc := range []struct{ query, code, message, position string }{
				{"DROP ROLE IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
				{"DROP USER IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
				{"DROP GROUP IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "12"},
				{`DROP ROLE IF EXISTS if,exists,"if","exists","Reader"`, "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
				{`DROP USER IF EXISTS "if",exists`, "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
				{`DROP GROUP IF EXISTS if,"exists"`, "0A000", "DROP ROLE IF EXISTS is unsupported", "12"},
				{"DROP ROLE IF EXISTS absent", "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
				{"/* é */ DROP ROLE IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "19"},
				{"INSERT INTO sentinel VALUES(9); DROP ROLE IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "43"},
				{"INSERT INTO sentinel VALUES(9); DROP USER IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "43"},
				{"INSERT INTO sentinel VALUES(9); DROP GROUP IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "44"},
				{"CREATE ROLE leaked; DROP USER IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "31"},
				{"DROP ROLE IF EXISTS", "42601", "syntax error at end of input", "20"},
				{"DROP USER IF EXISTS", "42601", "syntax error at end of input", "20"},
				{"DROP GROUP IF EXISTS", "42601", "syntax error at end of input", "21"},
				{"DROP ROLE IF reader", "42601", `syntax error at or near "reader"`, "14"},
				{"DROP ROLE IF EXISTS ,reader", "42601", `syntax error at or near ","`, "21"},
				{"DROP ROLE IF EXISTS reader,", "42601", "syntax error at end of input", "28"},
				{"DROP ROLE IF EXISTS 'reader'", "42601", `syntax error at or near "'reader'"`, "21"},
				{"DROP ROLE IF EXISTS $1", "42601", `syntax error at or near "$1"`, "21"},
				{"DROP ROLE IF EXISTS reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "28"},
				{"DROP ROLE reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "18"},
				{"DROP USER reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "18"},
				{"DROP GROUP reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "19"},
			} {
				for _, prepared := range []bool{false, true} {
					if backend.pg && tc.code == "0A000" {
						// PG performs conditional DROP; establish each spelling, execute it, and
						// recreate reader before checking retained peer identity and settings.
						readerOID := roleScenarioPostgresReaderOID(t, c)
						for _, name := range []string{"if", "exists", `"Reader"`} {
							roleScenarioEnsurePGRole(t, c, name)
						}
						if prepared && strings.Contains(tc.query, ";") {
							want := controlsReferenceDiagnostic("42601", "cannot insert multiple commands into a prepared statement")
							s := referenceErrorDiagnostic(tc.query, want)
							s.prepared = true
							s.errorStage = "prepare"
							referenceRunStep(t, p.ctx, c, backend.recorder, s)
						} else {
							s := referenceExec(tc.query, 0)
							s.prepared = prepared
							referenceRunStep(t, p.ctx, c, backend.recorder, s)
							if strings.HasPrefix(tc.query, "INSERT") {
								referenceRunStep(t, p.ctx, c, backend.recorder, referenceSelect("SELECT id FROM sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
								roleScenarioCommand(t, c, "DELETE FROM sentinel WHERE id=9", 1, false)
							}
							if strings.HasPrefix(tc.query, "CREATE ROLE leaked") {
								roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION leaked", 0, false)
								roleScenarioIdentity(t, c, "leaked", nil, true)
								roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
								roleScenarioCommand(t, c, "DROP ROLE leaked", 0, false)
							}
						}
						roleScenarioPostgresDroppedReader(t, p, c, peer, readerOID)
						for _, name := range []string{"reader", "if", "exists", `"Reader"`} {
							roleScenarioEnsurePGRole(t, c, name)
						}
					} else {
						want := controlsReferenceDiagnostic(tc.code, tc.message)
						want.Position = tc.position
						s := referenceErrorDiagnostic(tc.query, want)
						s.prepared = prepared
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, backend.recorder, s)
					}
					unchanged()
					want := controlsReferenceDiagnostic("42704", `role "leaked" does not exist`)
					referenceRunStep(t, p.ctx, c, backend.recorder, referenceErrorDiagnostic("DROP ROLE leaked", want))
				}
			}
			roleScenarioCommand(t, peer, "RESET SESSION AUTHORIZATION", 0, false)
			for _, prepared := range []bool{false, true} {
				for _, kind := range []string{"ROLE", "USER", "GROUP"} {
					for _, tc := range []struct {
						targets string
						removed []string
					}{{"if", []string{"if"}}, {"exists", []string{"exists"}}, {`"if"`, []string{"if"}}, {`"exists"`, []string{"exists"}}, {`if,exists,"if","exists","IF","EXISTS"`, []string{"if", "exists", `"IF"`, `"EXISTS"`}}} {
						if backend.pg {
							for _, q := range []string{`DROP ROLE IF EXISTS if`, `DROP ROLE IF EXISTS exists`, `DROP ROLE IF EXISTS "IF"`, `DROP ROLE IF EXISTS "EXISTS"`} {
								roleScenarioCommand(t, c, q, 0, false)
							}
						}
						for _, q := range []string{`CREATE ROLE if`, `CREATE ROLE exists`, `CREATE ROLE "IF"`, `CREATE ROLE "EXISTS"`} {
							roleScenarioCommand(t, c, q, 0, prepared)
						}
						roleScenarioCommand(t, c, "DROP "+kind+" "+tc.targets, 0, prepared)
						for _, name := range tc.removed {
							roleScenarioCommand(t, c, "CREATE ROLE "+name, 0, prepared)
						}
						roleScenarioCommand(t, c, `DROP ROLE if,exists,"IF","EXISTS"`, 0, prepared)
					}
				}
			}
		})
	}
}

func roleScenarioEnsurePGRole(t *testing.T, c *sql.Conn, name string) {
	t.Helper()
	var count int
	decoded := strings.Trim(name, `"`)
	if err := c.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_roles WHERE rolname=$1", decoded).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		roleScenarioCommand(t, c, "CREATE ROLE "+name, 0, false)
	} else if count != 1 {
		t.Fatal("role multiplicity", count)
	}
}

func roleScenarioPostgresReaderOID(t *testing.T, c *sql.Conn) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(context.Background(), "SELECT oid::bigint FROM pg_roles WHERE rolname='reader'").Scan(&oid); err != nil || oid <= 0 {
		t.Fatal("reader identity", oid, err)
	}
	return oid
}

func roleScenarioPostgresDroppedReader(t *testing.T, p *referencePair, admin, peer *sql.Conn, oldOID int64) {
	t.Helper()
	var retained int
	if err := admin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_roles WHERE oid=$1", oldOID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained == 1 {
		if oid := roleScenarioPostgresReaderOID(t, admin); oid != oldOID {
			t.Fatal("non-dropping command changed reader identity", oldOID, oid)
		}
		return
	}
	if retained != 0 {
		t.Fatal("reader OID multiplicity", retained)
	}
	// A live pq session keeps the dropped role's OID, not its reusable name.
	// Assert that distinct lifecycle before resetting the same physical owner.
	want := controlsReferenceDiagnostic("42704", fmt.Sprintf("invalid role OID: %d", oldOID))
	var session, current, repeated string
	err := peer.QueryRowContext(p.ctx, "SELECT session_user,current_user,session_user").Scan(&session, &current, &repeated)
	referenceAssertDiagnostic(t, err, &want)
	roleScenarioCommand(t, peer, "RESET SESSION AUTHORIZATION", 0, false)
	roleScenarioIdentity(t, peer, "stuntgres", nil, true)
	roleScenarioEnsurePGRole(t, admin, "reader")
	if oid := roleScenarioPostgresReaderOID(t, admin); oid == oldOID {
		t.Fatal("recreated role reused deleted identity", oldOID)
	}
	roleScenarioCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
	roleScenarioIdentity(t, peer, "reader", nil, true)
	var setting string
	if err := peer.QueryRowContext(p.ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "peer" {
		t.Fatal("same-owner recovery lost settings", setting, err)
	}
}
