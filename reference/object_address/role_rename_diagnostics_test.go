package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleRenameDiagnostics(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rr_diag", "rr_collision", "rr_guard", "rr_session", "rr_other"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, c, false, s) }
					for _, q := range []string{"CREATE ROLE rr_diag", "CREATE ROLE rr_collision", "CREATE ROLE rr_guard SUPERUSER", "CREATE ROLE rr_session SUPERUSER", "CREATE ROLE rr_other"} {
						run(fixture.Exec(q, 0))
					}
					for _, tc := range []struct{ query, state, message, detail string }{
						{"ALTER ROLE rr_absent RENAME TO rr_other", "42704", `role "rr_absent" does not exist`, ""},
						{"ALTER ROLE rr_diag RENAME TO rr_collision", "42710", `role "rr_collision" already exists`, ""},
						{"ALTER USER rr_diag RENAME TO rr_diag", "42710", `role "rr_diag" already exists`, ""},
						{"ALTER ROLE rr_diag RENAME TO pg_rr_reserved", "42939", `role name "pg_rr_reserved" is reserved`, `Role names starting with "pg_" are reserved.`},
						{"ALTER ROLE pg_read_all_data RENAME TO rr_other", "42939", `role name "pg_read_all_data" is reserved`, `Role names starting with "pg_" are reserved.`},
					} {
						s := fixture.RoleReferenceError(tc.query, tc.state, tc.message, tc.detail, route == "fresh", "execute")
						if route == "held" {
							fixture.RoleTransactionStep(t, p, b, c, true, s)
						} else {
							run(s)
						}
					}
					for _, target := range []string{"rr_session", "rr_guard"} {
						run(fixture.Exec("SET SESSION AUTHORIZATION rr_session", 0))
						run(fixture.Exec("SET ROLE rr_guard", 0))
						message := "current user cannot be renamed"
						if target == "rr_session" {
							message = "session user cannot be renamed"
						}
						s := fixture.RoleReferenceError("ALTER USER "+target+" RENAME TO rr_other", "0A000", message, "", route == "fresh", "execute")
						if route == "held" {
							fixture.RoleTransactionStep(t, p, b, c, true, s)
						} else {
							run(s)
						}
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					}
					for _, name := range []string{"rr_diag", "rr_collision", "rr_guard", "rr_session", "rr_other"} {
						run(fixture.RoleTransactionExists(name, true))
					}
				})
			}
		})
	}
}

func TestReferenceRoleRenameIdentifiers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			long := strings.Repeat("x", 62) + "é-tail"
			clipped := strings.Repeat("x", 62)
			fixture.RoleReferenceCleanup(t, []string{"rr_identifier", "Mixed \"Name", "rr_folded", clipped})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				var notices []fixture.Diagnostic
				_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, recorder, s) }
				run(fixture.Exec("CREATE ROLE rr_identifier", 0))
				run(fixture.Exec("ALTER ROLE rr_identifier RENAME TO "+pq.QuoteIdentifier("Mixed \"Name"), 0))
				run(fixture.RoleTransactionExists("Mixed \"Name", true))
				run(fixture.Exec("ALTER USER "+pq.QuoteIdentifier("Mixed \"Name")+" RENAME TO RR_FOLDED", 0))
				run(fixture.RoleTransactionExists("rr_folded", true))
				run(fixture.Exec("ALTER ROLE rr_folded RENAME TO "+pq.QuoteIdentifier(long), 0))
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + long + `" will be truncated to "` + clipped + `"`}
				if len(notices) != 1 || notices[0] != want {
					t.Fatalf("clipping notices=%#v want=%#v", notices, want)
				}
				run(fixture.RoleTransactionExists(clipped, true))
				run(fixture.RoleTransactionExists("rr_folded", false))
			}
		})
	}
}

func TestReferenceRoleRenameAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, create := range []bool{false, true} {
			for _, admin := range []bool{false, true} {
				for _, target := range []string{"ordinary", "replication", "superuser"} {
					t.Run(fmt.Sprintf("prepared=%v/create=%v/admin=%v/%s", prepared, create, admin, target), func(t *testing.T) {
						fixture.RoleReferenceCleanup(t, []string{"rr_actor", "rr_power", "rr_target", "rr_renamed"})
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
							run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, c, prepared, s) }
							attribute := ""
							if create {
								attribute = " CREATEROLE"
							}
							run(fixture.Exec("CREATE ROLE rr_actor"+attribute, 0))
							attribute = ""
							if target == "replication" {
								attribute = " REPLICATION"
							}
							if target == "superuser" {
								attribute = " SUPERUSER"
							}
							run(fixture.Exec("CREATE ROLE rr_target"+attribute, 0))
							if admin {
								run(fixture.Exec("GRANT rr_target TO rr_actor WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", 0))
							}
							run(fixture.Exec("SET SESSION AUTHORIZATION rr_actor", 0))
							query := "ALTER ROLE rr_target RENAME TO rr_renamed"
							allowed := create && admin && target != "superuser"
							if allowed {
								run(fixture.Exec(query, 0))
							} else {
								detail := `Only roles with the CREATEROLE attribute and the ADMIN option on role "rr_target" may rename this role.`
								if target == "superuser" {
									detail = "Only roles with the SUPERUSER attribute may rename roles with the SUPERUSER attribute."
								}
								run(fixture.RoleReferenceError(query, "42501", "permission denied to rename role", detail, prepared, "execute"))
							}
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							run(fixture.RoleTransactionExists("rr_target", !allowed))
							run(fixture.RoleTransactionExists("rr_renamed", allowed))
							if !allowed {
								run(fixture.Exec(query, 0))
							}
						}
					})
				}
			}
		}
	}
}
