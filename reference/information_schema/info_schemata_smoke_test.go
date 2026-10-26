package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoSchemataFeatureSmoke(t *testing.T) {
	iscScenario(t, func(c *iscCase) {
		c.exec("CREATE ROLE isc_owner", "CREATE ROLE isc_other_owner", "CREATE ROLE isc_reader", "CREATE SCHEMA isc_smoke AUTHORIZATION isc_owner", "REVOKE ALL ON SCHEMA isc_smoke FROM PUBLIC")
		q := iscQuery + " WHERE schema_name=$1"
		held := c.hold(q)
		check := func(name, owner string) {
			var rows [][]any
			if owner != "" {
				rows = c.row(name, owner)
			}
			c.check(held, c.projection(q, rows, name))
		}
		check("isc_smoke", "isc_owner")
		admin, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		publish := func(q string) { c.run(admin, fixture.Exec(q, 0)) }
		c.exec("SET SESSION AUTHORIZATION isc_reader")
		check("isc_smoke", "")
		publish("GRANT CREATE ON SCHEMA isc_smoke TO isc_reader")
		check("isc_smoke", "isc_owner")
		publish("REVOKE CREATE ON SCHEMA isc_smoke FROM isc_reader")
		check("isc_smoke", "")
		publish("GRANT USAGE ON SCHEMA isc_smoke TO isc_reader")
		check("isc_smoke", "isc_owner")
		publish("ALTER SCHEMA isc_smoke RENAME TO isc_renamed")
		check("isc_smoke", "")
		check("isc_renamed", "isc_owner")
		publish("ALTER SCHEMA isc_renamed OWNER TO isc_other_owner")
		check("isc_renamed", "isc_other_owner")
		publish("DROP SCHEMA isc_renamed")
		check("isc_renamed", "")
		c.check(held, c.projection(q, nil, nil))
		want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		want.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.schemata", want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(c.conn, s)
		}
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
