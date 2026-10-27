package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleExcludedCommands(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"owner", "reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			peer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			for _, q := range []string{"CREATE USER owner", "CREATE ROLE reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE sentinel(id INT)", "CREATE TABLE role(role INT)", "SET application_name TO 'SET ROLE reader'"} {
				if strings.HasPrefix(q, "CREATE TABLE") {
					fixture.WithPublicSchemaOwner(t, p, backend, "owner", func() { fixture.RoleScenarioCommand(t, c, q, 0, false) })
				} else {
					fixture.RoleScenarioCommand(t, c, q, 0, false)
				}
			}
			fixture.RoleScenarioCommand(t, c, "INSERT INTO sentinel VALUES(7)", 1, false)
			fixture.RoleScenarioCommand(t, c, "INSERT INTO role(role) VALUES(8)", 1, true)
			fixture.RoleScenarioCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
			fixture.RoleScenarioCommand(t, peer, "SET application_name TO peer", 0, true)
			unchanged := func() {
				t.Helper()
				fixture.RoleScenarioIdentity(t, c, "owner", nil, backend.PG)
				fixture.RoleScenarioIdentity(t, peer, "reader", nil, backend.PG)
				for _, tc := range []struct {
					c    *sql.Conn
					want string
				}{{c, "SET ROLE reader"}, {peer, "peer"}} {
					var got string
					if err := tc.c.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&got); err != nil || got != tc.want {
						t.Fatal(got, err)
					}
				}
				for _, tc := range []struct {
					q    string
					want int64
				}{{"SELECT id FROM sentinel", 7}, {"SELECT role FROM role WHERE role=8 ORDER BY role", 8}} {
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Select(tc.q, []string{map[int64]string{7: "id", 8: "role"}[tc.want]}, []string{"INT4"}, [][]any{{tc.want}}))
				}
			}
			unchanged()
			// Retain this definition's historical name, but LOCAL is now a real
			// selection boundary, not an excluded command.
			for _, query := range []string{
				"SET LOCAL ROLE reader",
				"SET LOCAL ROLE 'reader'",
				"SET LOCAL ROLE TO DEFAULT",
				"SET LOCAL ROLE = NONE",
				"INSERT INTO sentinel VALUES(9); SET LOCAL ROLE reader",
			} {
				for _, prepared := range []bool{false, true} {
					notices = nil
					s := fixture.Exec(query, 0)
					if prepared && strings.Contains(query, ";") {
						s = fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42601", "cannot insert multiple commands into a prepared statement"))
						s.ErrorStage = "prepare"
					} else if !strings.Contains(query, "DEFAULT") && !strings.Contains(query, "NONE") {
						s = fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", `permission denied to set role "reader"`))
						s.ErrorStage = "execute"
					}
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, record, s)
					if strings.Contains(query, ";") {
						if len(notices) != 0 {
							t.Fatal(notices)
						}
					} else {
						want := fixture.ControlsReferenceDiagnostic("25P01", "SET LOCAL can only be used in transaction blocks")
						want.Severity, want.SeverityUnlocalized = "WARNING", "WARNING"
						if len(notices) != 1 || notices[0] != want {
							t.Fatalf("notices %#v want %#v", notices, want)
						}
					}
					unchanged()
				}
			}
			for _, prepared := range []bool{false, true} {
				query := "INSERT INTO sentinel VALUES(9); SET LOCAL ROLE reader trailing"
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
				want.Position = "55"
				s := fixture.ErrorDiagnostic(query, want)
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, record, s)
				unchanged()
			}
			fixture.RoleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, true)
			bootstrap := "postgres"
			if backend.PG {
				bootstrap = "stuntgres"
			}
			fixture.RoleScenarioIdentity(t, c, bootstrap, nil, backend.PG)
			fixture.RoleScenarioIdentity(t, peer, "reader", nil, backend.PG)
			fixture.RoleScenarioCommand(t, peer, "RESET SESSION AUTHORIZATION", 0, false)
		})
	}
}

func TestReferenceRoleDropConditionalBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"reader", "leaked", "if", "exists", "IF", "EXISTS", "Reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			bootstrap := "postgres"
			if backend.PG {
				bootstrap = "stuntgres"
			}
			for _, q := range []string{"CREATE ROLE reader", "CREATE TABLE sentinel(id INT)", "SET application_name TO retained"} {
				fixture.RoleScenarioCommand(t, c, q, 0, false)
			}
			fixture.RoleScenarioCommand(t, c, "INSERT INTO sentinel VALUES(7)", 1, false)
			fixture.RoleScenarioCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
			fixture.RoleScenarioCommand(t, peer, "SET application_name TO peer", 0, false)
			unchanged := func() {
				t.Helper()
				fixture.RoleScenarioIdentity(t, c, bootstrap, nil, backend.PG)
				fixture.RoleScenarioIdentity(t, peer, "reader", nil, backend.PG)
				var value int64
				if err := c.QueryRowContext(p.Ctx, "SELECT id FROM sentinel").Scan(&value); err != nil || value != 7 {
					t.Fatal(value, err)
				}
				for _, tc := range []struct {
					c    *sql.Conn
					want string
				}{{c, "retained"}, {peer, "peer"}} {
					var got string
					if err := tc.c.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&got); err != nil || got != tc.want {
						t.Fatal(got, err)
					}
				}
				fixture.RoleScenarioCommand(t, c, "SET SESSION AUTHORIZATION reader", 0, false)
				fixture.RoleScenarioIdentity(t, c, "reader", nil, backend.PG)
				fixture.RoleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
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
					if backend.PG && tc.code == "0A000" {
						// PG performs conditional DROP; establish each spelling, execute it, and
						// recreate reader before checking retained peer identity and settings.
						readerOID := roleScenarioPostgresReaderOID(t, c)
						for _, name := range []string{"if", "exists", `"Reader"`} {
							roleScenarioEnsurePGRole(t, c, name)
						}
						if prepared && strings.Contains(tc.query, ";") {
							want := fixture.ControlsReferenceDiagnostic("42601", "cannot insert multiple commands into a prepared statement")
							s := fixture.ErrorDiagnostic(tc.query, want)
							s.Prepared = true
							s.ErrorStage = "prepare"
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
						} else {
							s := fixture.Exec(tc.query, 0)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
							if strings.HasPrefix(tc.query, "INSERT") {
								fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Select("SELECT id FROM sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
								fixture.RoleScenarioCommand(t, c, "DELETE FROM sentinel WHERE id=9", 1, false)
							}
							if strings.HasPrefix(tc.query, "CREATE ROLE leaked") {
								fixture.RoleScenarioCommand(t, c, "SET SESSION AUTHORIZATION leaked", 0, false)
								fixture.RoleScenarioIdentity(t, c, "leaked", nil, true)
								fixture.RoleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
								fixture.RoleScenarioCommand(t, c, "DROP ROLE leaked", 0, false)
							}
						}
						roleScenarioPostgresDroppedReader(t, p, c, peer, readerOID)
						for _, name := range []string{"reader", "if", "exists", `"Reader"`} {
							roleScenarioEnsurePGRole(t, c, name)
						}
					} else {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Position = tc.position
						s := fixture.ErrorDiagnostic(tc.query, want)
						s.Prepared = prepared
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					unchanged()
					want := fixture.ControlsReferenceDiagnostic("42704", `role "leaked" does not exist`)
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.ErrorDiagnostic("DROP ROLE leaked", want))
				}
			}
			fixture.RoleScenarioCommand(t, peer, "RESET SESSION AUTHORIZATION", 0, false)
			for _, prepared := range []bool{false, true} {
				for _, kind := range []string{"ROLE", "USER", "GROUP"} {
					for _, tc := range []struct {
						targets string
						removed []string
					}{{"if", []string{"if"}}, {"exists", []string{"exists"}}, {`"if"`, []string{"if"}}, {`"exists"`, []string{"exists"}}, {`if,exists,"if","exists","IF","EXISTS"`, []string{"if", "exists", `"IF"`, `"EXISTS"`}}} {
						if backend.PG {
							for _, q := range []string{`DROP ROLE IF EXISTS if`, `DROP ROLE IF EXISTS exists`, `DROP ROLE IF EXISTS "IF"`, `DROP ROLE IF EXISTS "EXISTS"`} {
								fixture.RoleScenarioCommand(t, c, q, 0, false)
							}
						}
						for _, q := range []string{`CREATE ROLE if`, `CREATE ROLE exists`, `CREATE ROLE "IF"`, `CREATE ROLE "EXISTS"`} {
							fixture.RoleScenarioCommand(t, c, q, 0, prepared)
						}
						fixture.RoleScenarioCommand(t, c, "DROP "+kind+" "+tc.targets, 0, prepared)
						for _, name := range tc.removed {
							fixture.RoleScenarioCommand(t, c, "CREATE ROLE "+name, 0, prepared)
						}
						fixture.RoleScenarioCommand(t, c, `DROP ROLE if,exists,"IF","EXISTS"`, 0, prepared)
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
		fixture.RoleScenarioCommand(t, c, "CREATE ROLE "+name, 0, false)
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

func roleScenarioPostgresDroppedReader(t *testing.T, p *fixture.Pair, admin, peer *sql.Conn, oldOID int64) {
	t.Helper()
	var retained int
	if err := admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE oid=$1", oldOID).Scan(&retained); err != nil {
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
	want := fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf("invalid role OID: %d", oldOID))
	var session, current, repeated string
	err := peer.QueryRowContext(p.Ctx, "SELECT session_user,current_user,session_user").Scan(&session, &current, &repeated)
	fixture.AssertDiagnostic(t, err, &want)
	fixture.RoleScenarioCommand(t, peer, "RESET SESSION AUTHORIZATION", 0, false)
	fixture.RoleScenarioIdentity(t, peer, "stuntgres", nil, true)
	roleScenarioEnsurePGRole(t, admin, "reader")
	if oid := roleScenarioPostgresReaderOID(t, admin); oid == oldOID {
		t.Fatal("recreated role reused deleted identity", oldOID)
	}
	fixture.RoleScenarioCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
	fixture.RoleScenarioIdentity(t, peer, "reader", nil, true)
	var setting string
	if err := peer.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "peer" {
		t.Fatal("same-owner recovery lost settings", setting, err)
	}
}
