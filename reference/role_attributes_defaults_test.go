package reference_test

import "testing"

// Defaults and replacement fields follow REL_18_0 user.c CreateRole/AlterRole.
func TestReferenceRoleAttributesDefaultsAndAliases(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			names := []string{"rad_role", "rad_user", "rad_group", "rad_override"}
			roleReferenceCleanup(t, names)
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				run := func(s referenceStep) { roleAttributesRun(t, p, b, b.db, prepared, s) }
				for i, kind := range []string{"ROLE", "USER", "GROUP", "USER"} {
					options := ""
					if i == 3 {
						options = " NOLOGIN"
					}
					run(referenceExec("CREATE "+kind+" "+names[i]+options, 0))
					if b.pg {
						roleAttributesFacts(t, p, names[i], roleAttributesDefaults(i == 1))
					}
				}
				positive := []string{"SUPERUSER", "CREATEDB", "CREATEROLE", "LOGIN", "INHERIT", "REPLICATION", "BYPASSRLS"}
				for i, attr := range positive {
					run(referenceExec("ALTER ROLE rad_role "+attr, 0))
					want := roleAttributesDefaults(false)
					want[i] = true
					if b.pg {
						roleAttributesFacts(t, p, names[0], want)
					}
					run(referenceExec("ALTER ROLE rad_role NO"+attr, 0))
					want[i] = false
					if b.pg {
						roleAttributesFacts(t, p, names[0], want)
					}
					// Restore defaults between independent toggles.
					run(referenceExec("ALTER ROLE rad_role INHERIT", 0))
				}
				run(referenceExec("ALTER ROLE rad_override CREATEDB CREATEROLE NOINHERIT REPLICATION BYPASSRLS CONNECTION LIMIT 9 PASSWORD 'role-attributes-fixture' VALID UNTIL '2030-01-02 03:04:05+00'", 0))
				run(referenceExec("ALTER USER rad_override NOCREATEDB", 0))
				if b.pg {
					roleAttributesFacts(t, p, "rad_override", []any{false, false, true, false, false, true, true, int64(9), false, "2030-01-02 03:04:05+00"})
				}
				for _, spec := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
					run(referenceExec("ALTER ROLE "+spec+" CONNECTION LIMIT -1", 0))
				}
				// PostgreSQL accepts an empty ALTER option list; it must not reset LOGIN.
				run(referenceExec("ALTER USER rad_override", 0))
				if b.pg {
					roleAttributesFacts(t, p, "rad_override", []any{false, false, true, false, false, true, true, int64(9), false, "2030-01-02 03:04:05+00"})
				}
			}
		})
	}
}
