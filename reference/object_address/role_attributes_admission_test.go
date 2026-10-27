package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleAttributesConnectionAdmission(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ral_actor", "ral_other", "ral_super"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, b.DB, prepared, s) }
				for _, q := range []string{"CREATE ROLE ral_actor LOGIN CONNECTION LIMIT 1 PASSWORD 'role-attributes-fixture'", "CREATE ROLE ral_other LOGIN CONNECTION LIMIT 1 PASSWORD 'role-attributes-fixture'", "CREATE ROLE ral_super LOGIN SUPERUSER CONNECTION LIMIT 0 PASSWORD 'role-attributes-fixture'"} {
					run(fixture.Exec(q, 0))
				}
				first, err := fixture.RoleAttributesPhysical(t, p, b, "ral_actor")
				if err != nil {
					t.Fatal(err)
				}
				defer first.Close()
				denied := func(name string) {
					c, err := fixture.RoleAttributesPhysical(t, p, b, name)
					if c != nil {
						c.Close()
						t.Fatal("denied physical connection returned handle")
					}
					want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: `too many connections for role "` + name + `"`}
					fixture.AssertDiagnostic(t, err, &want)
				}
				denied("ral_actor")
				other, err := fixture.RoleAttributesPhysical(t, p, b, "ral_other")
				if err != nil {
					t.Fatal(err)
				}
				other.Close()
				// Lowering a limit does not terminate its incumbent.
				run(fixture.Exec("ALTER ROLE ral_actor CONNECTION LIMIT 0", 0))
				denied("ral_actor")
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
				denied("ral_actor")
				run(fixture.Exec("ALTER ROLE ral_actor CONNECTION LIMIT +1", 0))
				pool := fixture.RoleAttributesPool(t, p, b, "ral_actor")
				pool.SetMaxOpenConns(1)
				pool.SetMaxIdleConns(1)
				c, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RoleScenarioIdentity(t, c, "ral_actor", nil, b.PG)
				c.Close() // returns the same physical session to idle; does not free slot
				denied("ral_actor")
				c, err = pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RoleScenarioIdentity(t, c, "ral_actor", nil, b.PG)
				c.Close()
				if err := pool.Close(); err != nil {
					t.Fatal(err)
				}
				replacement, err := fixture.RoleAttributesPhysical(t, p, b, "ral_actor")
				if err != nil {
					t.Fatal(err)
				}
				replacement.Close()
				for _, limit := range []string{"2147483647", "-1"} {
					run(fixture.Exec("ALTER ROLE ral_actor CONNECTION LIMIT "+limit, 0))
					a, err := fixture.RoleAttributesPhysical(t, p, b, "ral_actor")
					if err != nil {
						t.Fatal(err)
					}
					z, err := fixture.RoleAttributesPhysical(t, p, b, "ral_actor")
					if err != nil {
						a.Close()
						t.Fatal(err)
					}
					z.Close()
					a.Close()
				}
				superPool := fixture.RoleAttributesPool(t, p, b, "ral_super")
				super, err := superPool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer super.Close()
				// Accounting remains the original authenticator through identity changes.
				fixture.RoleAttributesRun(t, p, b, super, prepared, fixture.Exec("SET SESSION AUTHORIZATION ral_other", 0))
				other, err = fixture.RoleAttributesPhysical(t, p, b, "ral_other")
				if err != nil {
					t.Fatal("identity switch reassigned admission slot", err)
				}
				other.Close()
				fixture.RoleAttributesRun(t, p, b, super, prepared, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				s2, err := fixture.RoleAttributesPhysical(t, p, b, "ral_super")
				if err != nil {
					t.Fatal(err)
				}
				s2.Close()
				run(fixture.Exec("ALTER ROLE ral_super NOLOGIN", 0))
				noLogin, err := fixture.RoleAttributesPhysical(t, p, b, "ral_super")
				if noLogin != nil {
					noLogin.Close()
				}
				want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "ral_super" is not permitted to log in`}
				fixture.AssertDiagnostic(t, err, &want)
				fixture.RoleScenarioIdentity(t, super, "ral_super", nil, b.PG)
			}
		})
	}
}
