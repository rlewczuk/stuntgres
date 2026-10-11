package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func roleAttributesPriorityQuestions(t *testing.T, prepared bool) {
	cases := []struct{ actor, query, code, message, detail string }{
		{"ordinary", "CREATE ROLE rap_absent LOGIN NOLOGIN", "42601", "conflicting or redundant options", ""},
		{"ordinary", "CREATE ROLE rap_absent CONNECTION LIMIT -2", "22023", "invalid connection limit: -2", ""},
		{"ordinary", "CREATE ROLE pg_rap_reserved VALID UNTIL 'bad'", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles."},
		{"ordinary", "CREATE ROLE rap_target VALID UNTIL 'bad'", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles."},
		{"ordinary", "CREATE ROLE rap_absent VALID UNTIL 'bad'", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles."},
		{"ordinary", "ALTER ROLE rap_absent VALID UNTIL 'bad'", "42704", `role "rap_absent" does not exist`, ""},
		{"ordinary", "ALTER ROLE rap_target VALID UNTIL 'bad'", "42501", "permission denied to alter role", `Only roles with the CREATEROLE attribute and the ADMIN option on role "rap_target" may alter this role.`},
		{"ordinary", "ALTER ROLE rap_absent CONNECTION LIMIT -2", "22023", "invalid connection limit: -2", ""},
		{"ordinary", "CREATE ROLE rap_absent SUPERUSER", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles."},
		{"creator", "CREATE ROLE rap_absent SUPERUSER", "42501", "permission denied to create role", "Only roles with the SUPERUSER attribute may create roles with the SUPERUSER attribute."},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("priority/%s/%s/prepared%v", tc.actor, tc.query, prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rap_actor", "rap_target", "rap_absent", "pg_rap_reserved"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run := func(s fixture.Step) { roleAttributesRun(t, p, b, c, prepared, s) }
				options := ""
				if tc.actor == "creator" {
					options = " CREATEROLE"
				}
				run(fixture.Exec("CREATE ROLE rap_actor"+options, 0))
				run(fixture.Exec("CREATE ROLE rap_target", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION rap_actor", 0))
				s := fixture.RoleReferenceError(tc.query, tc.code, tc.message, tc.detail, prepared, "execute")
				if tc.code == "42601" {
					s.Diagnostic.Position = "30"
				}
				run(s)
				if b.PG {
					roleAttributesFacts(t, p, "rap_target", roleAttributesDefaults(false))
				}
			}
		})
	}
}
