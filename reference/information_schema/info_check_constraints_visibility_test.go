package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintsVisibility(t *testing.T) {
	t.Run("enabled_owner_only", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("CREATE ROLE icc_owner", "CREATE ROLE icc_reader", "CREATE ROLE icc_inherit", "CREATE ROLE icc_set", "CREATE ROLE icc_routine_owner", "CREATE ROLE icc_power SUPERUSER",
				"GRANT icc_owner TO icc_inherit WITH INHERIT TRUE", "GRANT icc_owner TO icc_set WITH INHERIT FALSE, SET TRUE",
				"CREATE SCHEMA icc_private", "REVOKE ALL ON SCHEMA icc_private FROM PUBLIC",
				"CREATE FUNCTION icc_private.icc_f(integer) RETURNS boolean RETURN $1>0",
				"CREATE TABLE icc_private.icc_t(v integer CONSTRAINT icc_visible CHECK(icc_private.icc_f(v)))",
				"CREATE DOMAIN icc_private.icc_d AS integer CONSTRAINT icc_visible_dom CHECK(icc_private.icc_f(VALUE))",
				"ALTER TABLE icc_private.icc_t OWNER TO icc_owner", "ALTER DOMAIN icc_private.icc_d OWNER TO icc_owner",
				"ALTER FUNCTION icc_private.icc_f(integer) OWNER TO icc_routine_owner",
				"REVOKE ALL ON FUNCTION icc_private.icc_f(integer) FROM PUBLIC",
				"REVOKE ALL ON DOMAIN icc_private.icc_d FROM PUBLIC")
			want := [][]any{c.row("icc_private", "icc_visible", "icc_private.icc_f(v)"), c.row("icc_private", "icc_visible_dom", "icc_private.icc_f(VALUE)")}
			check := c.consumer()
			for _, actor := range []struct {
				role    string
				visible bool
			}{{"icc_owner", true}, {"icc_inherit", true}, {"icc_power", true}, {"icc_set", false}, {"icc_reader", false}, {"icc_routine_owner", false}} {
				c.exec("SET SESSION AUTHORIZATION " + actor.role)
				var rows [][]any
				if actor.visible {
					rows = want
				}
				check(rows...)
				c.missingColumn()
				c.exec("RESET SESSION AUTHORIZATION")
			}
			c.exec("SET SESSION AUTHORIZATION icc_set")
			check()
			c.exec("SET ROLE icc_owner")
			check(want...)
			c.exec("RESET ROLE")
			check()
			c.exec("RESET SESSION AUTHORIZATION")
			for _, grant := range []string{
				"GRANT SELECT ON icc_private.icc_t TO icc_reader",
				"GRANT SELECT(v) ON icc_private.icc_t TO icc_reader",
				"GRANT USAGE ON DOMAIN icc_private.icc_d TO icc_reader WITH GRANT OPTION",
				"GRANT USAGE ON DOMAIN icc_private.icc_d TO PUBLIC",
				"GRANT EXECUTE ON FUNCTION icc_private.icc_f(integer) TO icc_reader",
				"GRANT USAGE ON SCHEMA icc_private TO icc_reader",
			} {
				c.exec(grant, "SET SESSION AUTHORIZATION icc_reader")
				check()
				c.exec("RESET SESSION AUTHORIZATION")
			}
			c.exec("REVOKE USAGE ON SCHEMA icc_private FROM icc_reader", "SET SESSION AUTHORIZATION icc_owner")
			check(want...)
			c.exec("RESET SESSION AUTHORIZATION", "REVOKE SELECT ON icc_private.icc_t FROM icc_reader", "REVOKE SELECT(v) ON icc_private.icc_t FROM icc_reader", "REVOKE USAGE ON DOMAIN icc_private.icc_d FROM icc_reader", "REVOKE USAGE ON DOMAIN icc_private.icc_d FROM PUBLIC", "REVOKE EXECUTE ON FUNCTION icc_private.icc_f(integer) FROM icc_reader", "SET SESSION AUTHORIZATION icc_owner")
			check(want...)
			c.exec("RESET SESSION AUTHORIZATION")

			admin, err := c.b.DB.Conn(c.p.Ctx)
			if err != nil {
				c.t.Fatal(err)
			}
			defer admin.Close()
			publish := func(q string) { fixture.RunStep(c.t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec(q, 0)) }
			c.exec("SET SESSION AUTHORIZATION icc_reader")
			check()
			publish("GRANT icc_owner TO icc_reader WITH INHERIT TRUE")
			check(want...)
			publish("REVOKE icc_owner FROM icc_reader")
			check()
			publish("ALTER TABLE icc_private.icc_t OWNER TO icc_reader")
			check(want[0])
			publish("ALTER DOMAIN icc_private.icc_d OWNER TO icc_reader")
			check(want...)
			publish("ALTER TABLE icc_private.icc_t OWNER TO icc_owner")
			check(want[1])
			publish("ALTER DOMAIN icc_private.icc_d OWNER TO icc_owner")
			check()
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("other_session_temporary", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			owner, err := c.b.DB.Conn(c.p.Ctx)
			if err != nil {
				c.t.Fatal(err)
			}
			defer owner.Close()
			fixture.RunStep(c.t, c.p.Ctx, owner, c.b.Recorder, fixture.Exec("CREATE TEMP TABLE icc_temp(v integer CONSTRAINT icc_temp_ck CHECK(v>0))", 0))
			defer owner.ExecContext(c.p.Ctx, "DROP TABLE icc_temp")
			var schema string
			if err := owner.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='icc_temp'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			c.consumer()(c.row(schema, "icc_temp_ck", "(v > 0)"))
			c.exec("CREATE ROLE icc_reader", "SET SESSION AUTHORIZATION icc_reader")
			c.consumer()()
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
}
