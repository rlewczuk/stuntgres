package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Defaults and replacement fields follow REL_18_0 user.c CreateRole/AlterRole.
func TestReferenceRoleAttributesDefaultsAndAliases(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			names := []string{"rad_role", "rad_user", "rad_group", "rad_override"}
			fixture.RoleReferenceCleanup(t, names)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				run := func(s fixture.Step) { roleAttributesRun(t, p, b, b.DB, prepared, s) }
				for i, kind := range []string{"ROLE", "USER", "GROUP", "USER"} {
					options := ""
					if i == 3 {
						options = " NOLOGIN"
					}
					run(fixture.Exec("CREATE "+kind+" "+names[i]+options, 0))
					if b.PG {
						roleAttributesFacts(t, p, names[i], roleAttributesDefaults(i == 1))
					}
				}
				positive := []string{"SUPERUSER", "CREATEDB", "CREATEROLE", "LOGIN", "INHERIT", "REPLICATION", "BYPASSRLS"}
				for i, attr := range positive {
					run(fixture.Exec("ALTER ROLE rad_role "+attr, 0))
					want := roleAttributesDefaults(false)
					want[i] = true
					if b.PG {
						roleAttributesFacts(t, p, names[0], want)
					}
					run(fixture.Exec("ALTER ROLE rad_role NO"+attr, 0))
					want[i] = false
					if b.PG {
						roleAttributesFacts(t, p, names[0], want)
					}
					// Restore defaults between independent toggles.
					run(fixture.Exec("ALTER ROLE rad_role INHERIT", 0))
				}
				run(fixture.Exec("ALTER ROLE rad_override CREATEDB CREATEROLE NOINHERIT REPLICATION BYPASSRLS CONNECTION LIMIT 9 PASSWORD 'role-attributes-fixture' VALID UNTIL '2030-01-02 03:04:05+00'", 0))
				run(fixture.Exec("ALTER USER rad_override NOCREATEDB", 0))
				if b.PG {
					roleAttributesFacts(t, p, "rad_override", []any{false, false, true, false, false, true, true, int64(9), false, "2030-01-02 03:04:05+00"})
				}
				for _, spec := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
					run(fixture.Exec("ALTER ROLE "+spec+" CONNECTION LIMIT -1", 0))
				}
				// PostgreSQL accepts an empty ALTER option list; it must not reset LOGIN.
				run(fixture.Exec("ALTER USER rad_override", 0))
				if b.PG {
					roleAttributesFacts(t, p, "rad_override", []any{false, false, true, false, false, true, true, int64(9), false, "2030-01-02 03:04:05+00"})
				}
			}
		})
	}
}
