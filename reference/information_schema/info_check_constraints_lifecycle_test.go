package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintsLifecycle(t *testing.T) {
	t.Run("definitions_and_identity", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("CREATE TABLE icc_life(v integer)", "CREATE DOMAIN icc_life_d AS integer", "CREATE SCHEMA icc_moved")
			check := c.consumer()
			check()
			c.exec("ALTER TABLE icc_life ADD CONSTRAINT icc_life_ck CHECK(v>0) NOT VALID", "ALTER TABLE icc_life ADD CONSTRAINT icc_life_nn NOT NULL v NOT VALID", "ALTER DOMAIN icc_life_d ADD CONSTRAINT icc_life_dom CHECK(VALUE>0) NOT VALID")
			a, n, d := c.row("public", "icc_life_ck", "(v > 0)"), c.row("public", "icc_life_nn", "v IS NOT NULL"), c.row("public", "icc_life_dom", "(VALUE > 0)")
			check(a, n, d)
			aOID, nOID, dOID := c.constraintOID("icc_life_ck"), c.constraintOID("icc_life_nn"), c.constraintOID("icc_life_dom")
			if aOID == nOID || aOID == dOID || nOID == dOID {
				c.t.Fatal("constraint identities were collapsed")
			}
			for _, name := range []string{"icc_life_ck", "icc_life_nn", "icc_life_dom"} {
				c.flags(name, false)
			}
			c.exec("ALTER TABLE icc_life VALIDATE CONSTRAINT icc_life_ck", "ALTER TABLE icc_life VALIDATE CONSTRAINT icc_life_nn", "ALTER DOMAIN icc_life_d VALIDATE CONSTRAINT icc_life_dom")
			check(a, n, d)
			for _, name := range []string{"icc_life_ck", "icc_life_nn", "icc_life_dom"} {
				c.flags(name, true)
			}
			c.exec("ALTER DOMAIN icc_life_d SET NOT NULL")
			var domainNN string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT conname FROM pg_constraint WHERE contypid='icc_life_d'::regtype AND contype='n'").Scan(&domainNN); err != nil {
				c.t.Fatal(err)
			}
			check(a, n, d, c.row("public", domainNN, "VALUE IS NOT NULL"))
			c.exec("ALTER DOMAIN icc_life_d DROP NOT NULL")
			check(a, n, d)
			c.exec("BEGIN", "ALTER DOMAIN icc_life_d ADD CONSTRAINT icc_domain_rollback CHECK(VALUE<100) NOT VALID")
			check(a, n, d, c.row("public", "icc_domain_rollback", "(VALUE < 100)"))
			c.flags("icc_domain_rollback", false)
			c.exec("ALTER DOMAIN icc_life_d VALIDATE CONSTRAINT icc_domain_rollback")
			c.flags("icc_domain_rollback", true)
			check(a, n, d, c.row("public", "icc_domain_rollback", "(VALUE < 100)"))
			c.exec("ALTER DOMAIN icc_life_d SET NOT NULL")
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT conname FROM pg_constraint WHERE contypid='icc_life_d'::regtype AND contype='n'").Scan(&domainNN); err != nil {
				c.t.Fatal(err)
			}
			check(a, n, d, c.row("public", "icc_domain_rollback", "(VALUE < 100)"), c.row("public", domainNN, "VALUE IS NOT NULL"))
			c.exec("ROLLBACK")
			check(a, n, d)
			if c.constraintOID("icc_life_dom") != dOID {
				c.t.Fatal("domain rollback replaced original constraint identity")
			}
			c.flags("icc_life_dom", true)
			c.exec("BEGIN", "ALTER TABLE icc_life RENAME CONSTRAINT icc_life_ck TO icc_rollback")
			check(c.row("public", "icc_rollback", "(v > 0)"), n, d)
			c.exec("ROLLBACK")
			check(a, n, d)
			if c.constraintOID("icc_life_ck") != aOID {
				c.t.Fatal("rollback replaced original constraint identity")
			}
			c.exec("BEGIN", "ALTER TABLE icc_life RENAME CONSTRAINT icc_life_ck TO icc_renamed", "COMMIT", "ALTER TABLE icc_life RENAME COLUMN v TO value_new")
			a, n = c.row("public", "icc_renamed", "(value_new > 0)"), c.row("public", "icc_life_nn", "value_new IS NOT NULL")
			check(a, n, d)
			c.exec("ALTER TABLE icc_life RENAME TO icc_life_new")
			check(a, n, d)
			c.exec("ALTER TABLE icc_life_new SET SCHEMA icc_moved", "ALTER DOMAIN icc_life_d SET SCHEMA icc_moved")
			a, n, d = c.row("icc_moved", "icc_renamed", "(value_new > 0)"), c.row("icc_moved", "icc_life_nn", "value_new IS NOT NULL"), c.row("icc_moved", "icc_life_dom", "(VALUE > 0)")
			check(a, n, d)
			c.exec("ALTER DOMAIN icc_moved.icc_life_d RENAME TO icc_life_domain_new")
			check(a, n, d)
			q := `SELECT c.oid::bigint,c.conname,n.nspname,r.relname,t.typname,c.convalidated,c.conenforced FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace LEFT JOIN pg_class r ON r.oid=c.conrelid LEFT JOIN pg_type t ON t.oid=c.contypid WHERE c.oid=$1::oid OR c.oid=$2::oid OR c.oid=$3::oid`
			s := fixture.Select(q, []string{"oid", "conname", "nspname", "relname", "typname", "convalidated", "conenforced"}, []string{"INT8", "NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL"}, [][]any{{aOID, "icc_renamed", "icc_moved", "icc_life_new", nil, true, true}, {nOID, "icc_life_nn", "icc_moved", "icc_life_new", nil, true, true}, {dOID, "icc_life_dom", "icc_moved", nil, "icc_life_domain_new", true, true}}, aOID, nOID, dOID)
			s.Bag = true
			c.run(s)
			c.run(fixture.Prepared(s))
			c.exec("ALTER TABLE icc_moved.icc_life_new DROP CONSTRAINT icc_life_nn")
			check(a, d)
			c.exec("ALTER DOMAIN icc_moved.icc_life_domain_new DROP CONSTRAINT icc_life_dom")
			check(a)
			c.exec("DROP TABLE icc_moved.icc_life_new", "DROP DOMAIN icc_moved.icc_life_domain_new")
			check()
		})
	})
	t.Run("routine_and_settings", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("CREATE SCHEMA icc_moved", "CREATE FUNCTION icc_f(integer) RETURNS boolean RETURN $1>0", "CREATE TABLE icc_calls(v integer CONSTRAINT icc_calls CHECK(icc_f(v) AND icc_f(v)), CONSTRAINT icc_calls_nn CHECK(v IS NOT NULL))", "CREATE DOMAIN icc_calls_d AS integer CONSTRAINT icc_calls_dom CHECK(icc_f(VALUE))")
			check := c.consumer()
			rows := func(fn, variable string) [][]any {
				return [][]any{c.row("public", "icc_calls", "("+fn+"("+variable+") AND "+fn+"("+variable+"))"), c.row("public", "icc_calls_nn", "("+variable+" IS NOT NULL)"), c.row("public", "icc_calls_dom", fn+"(VALUE)")}
			}
			check(rows("icc_f", "v")...)
			iccRoutineWitness(c, "public", "icc_f")
			before := rsuIdentity(c.t, c.p, c.conn, "public", "icc_f", "integer")
			c.exec("ALTER FUNCTION icc_f(integer) RENAME TO icc_new")
			after := rsuIdentity(c.t, c.p, c.conn, "public", "icc_new", "integer")
			if after != "icc_new"+before[len("icc_f"):] {
				c.t.Fatalf("routine identity changed: %s -> %s", before, after)
			}
			check(rows("icc_new", "v")...)
			iccRoutineWitness(c, "public", "icc_new")
			c.exec("ALTER FUNCTION icc_new(integer) SET SCHEMA icc_moved")
			check(rows("icc_moved.icc_new", "v")...)
			iccRoutineWitness(c, "icc_moved", "icc_new")
			c.exec("SET search_path=public,icc_moved")
			check(rows("icc_new", "v")...)
			c.exec("SET quote_all_identifiers=on")
			check(rows(`"icc_new"`, `"v"`)...)
			c.exec("SET quote_all_identifiers=off", "DROP FUNCTION icc_moved.icc_new(integer) CASCADE")
			check(c.row("public", "icc_calls_nn", "(v IS NOT NULL)"))
			c.run(fixture.Select("SELECT constraint_name FROM information_schema.check_constraint_routine_usage WHERE constraint_name IN ('icc_calls','icc_calls_dom')", []string{"constraint_name"}, []string{"NAME"}, nil))
			c.exec("DROP DOMAIN icc_calls_d CASCADE", "DROP TABLE icc_calls")
			check()
		})
	})
	t.Run("output_settings", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("SET DateStyle='ISO, MDY'", "CREATE TABLE icc_date(v date CONSTRAINT icc_date CHECK(v > DATE '2024-01-02'))")
			check := c.consumer()
			check(c.row("public", "icc_date", "(v > '2024-01-02'::date)"))
			c.exec("SET DateStyle='SQL, DMY'")
			check(c.row("public", "icc_date", "(v > '02/01/2024'::date)"))
			c.exec("SET DateStyle='ISO, MDY'")
			check(c.row("public", "icc_date", "(v > '2024-01-02'::date)"))
		})
	})
	t.Run("raw_not_null_and_string_settings", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("SET standard_conforming_strings=on", `CREATE TABLE icc_text("Odd name" text CONSTRAINT icc_text_nn NOT NULL, CONSTRAINT icc_text_ck CHECK("Odd name" = 'a''b\c'))`)
			check := c.consumer()
			check(c.row("public", "icc_text_nn", "Odd name IS NOT NULL"), c.row("public", "icc_text_ck", `("Odd name" = 'a''b\c'::text)`))
			c.exec("SET quote_all_identifiers=on")
			check(c.row("public", "icc_text_nn", "Odd name IS NOT NULL"), c.row("public", "icc_text_ck", `("Odd name" = 'a''b\c'::"text")`))
			c.exec("SET standard_conforming_strings=off")
			check(c.row("public", "icc_text_nn", "Odd name IS NOT NULL"), c.row("public", "icc_text_ck", `("Odd name" = 'a''b\\c'::"text")`))
			c.exec("SET standard_conforming_strings=on", "SET quote_all_identifiers=off")
		})
	})
}
