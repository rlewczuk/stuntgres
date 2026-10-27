package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func roleAttributesValidityQuestions(t *testing.T, prepared bool) {
	for _, tc := range []struct{ label, setting, expiry, want string }{
		{"session-zone", "SET TimeZone TO 'America/New_York'", "2030-01-02 03:04:05", "2030-01-02 08:04:05+00"},
		{"date-style", "SET DateStyle TO 'ISO, DMY'", "02/03/2030 00:00:00+00", "2030-03-02 00:00:00+00"},
		{"abbreviations", "SET timezone_abbreviations TO 'Australia'", "2030-01-02 00:00:00 EST", "2030-01-01 14:00:00+00"},
	} {
		t.Run(fmt.Sprintf("validity/%s/prepared%v", tc.label, prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rav_actor"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, c, prepared, s) }
				run(fixture.Exec("CREATE ROLE rav_actor LOGIN PASSWORD NULL", 0))
				held, err := c.PrepareContext(p.Ctx, "ALTER USER rav_actor VALID UNTIL '"+tc.expiry+"'")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				run(fixture.Exec(tc.setting, 0))
				// Both direct and true Prepare routes use the execution environment.
				if prepared {
					if _, err := held.ExecContext(p.Ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					run(fixture.Exec("ALTER ROLE rav_actor VALID UNTIL '"+tc.expiry+"'", 0))
				}
				if b.PG {
					want := roleAttributesDefaults(true)
					want[9] = tc.want
					roleAttributesFacts(t, p, "rav_actor", want)
				}
			}
		})
	}
	t.Run(fmt.Sprintf("trusted-expired-no-password/prepared%v", prepared), func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rav_expired"})
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			fixture.RoleAttributesRun(t, p, b, b.DB, prepared, fixture.Exec("CREATE ROLE rav_expired LOGIN PASSWORD NULL VALID UNTIL '-infinity'", 0))
			if b.PG {
				want := roleAttributesDefaults(true)
				want[9] = "-infinity"
				roleAttributesFacts(t, p, "rav_expired", want)
			} else {
				// Trusted host assertion has no password handshake. Do not claim
				// pq password authentication can admit this expired/no-password role.
				c, err := fixture.RoleAttributesPhysical(t, p, b, "rav_expired")
				if err != nil {
					t.Fatal(err)
				}
				c.Close()
			}
		}
	})
}
