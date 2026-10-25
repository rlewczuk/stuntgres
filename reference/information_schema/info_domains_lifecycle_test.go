package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainsLifecycle(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		c.exec("CREATE SCHEMA idm_scope", "CREATE SCHEMA idm_moved")
		where := "domain_schema=$1 AND domain_name=$2"
		h := c.hold(where)
		check := func(schema, name string, want ...[]any) { c.held(h, where, []any{schema, name}, want...) }
		check("idm_scope", "sample")
		c.exec("CREATE DOMAIN idm_scope.sample AS integer DEFAULT 7 CONSTRAINT positive CHECK(VALUE>0) NOT NULL")
		row := idmInteger(c.database, "idm_scope", "sample", "7")
		check("idm_scope", "sample", row)
		for _, change := range []struct {
			sql         string
			defaultText any
		}{
			{"ALTER DOMAIN idm_scope.sample SET DEFAULT 11", "11"},
			{"ALTER DOMAIN idm_scope.sample DROP DEFAULT", nil},
		} {
			c.exec(change.sql)
			row[18] = change.defaultText
			check("idm_scope", "sample", row)
		}
		constraint := func(name string, present, valid bool) {
			var want [][]any
			if present {
				want = [][]any{{valid}}
			}
			c.run(fixture.Select("SELECT convalidated FROM pg_catalog.pg_constraint WHERE contypid='idm_scope.sample'::regtype::oid AND conname='"+name+"'", []string{"convalidated"}, []string{"BOOL"}, want))
		}
		constraint("positive", true, true)
		c.exec("ALTER DOMAIN idm_scope.sample DROP CONSTRAINT positive")
		constraint("positive", false, false)
		check("idm_scope", "sample", row)
		c.exec("ALTER DOMAIN idm_scope.sample ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID")
		constraint("positive", true, false)
		check("idm_scope", "sample", row)
		c.exec("ALTER DOMAIN idm_scope.sample VALIDATE CONSTRAINT positive")
		constraint("positive", true, true)
		check("idm_scope", "sample", row)
		for _, notNull := range []bool{false, true} {
			ddl := "DROP NOT NULL"
			if notNull {
				ddl = "SET NOT NULL"
			}
			c.exec("ALTER DOMAIN idm_scope.sample " + ddl)
			c.run(fixture.Select("SELECT typnotnull FROM pg_catalog.pg_type WHERE oid='idm_scope.sample'::regtype", []string{"typnotnull"}, []string{"BOOL"}, [][]any{{notNull}}))
			check("idm_scope", "sample", row)
		}
		c.exec("ALTER DOMAIN idm_scope.sample RENAME TO renamed")
		row[2] = "renamed"
		check("idm_scope", "sample")
		check("idm_scope", "renamed", row)
		c.exec("ALTER DOMAIN idm_scope.renamed SET SCHEMA idm_moved")
		row[1] = "idm_moved"
		check("idm_scope", "renamed")
		check("idm_moved", "renamed", row)
		c.exec("DROP DOMAIN idm_moved.renamed")
		check("idm_moved", "renamed")
	})
}

func TestReferenceInfoDomainsDiagnostics(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.domains", d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(s)
		}
		c.exec("CREATE ROLE idm_reader", "CREATE SCHEMA idm_scope", "GRANT USAGE ON SCHEMA idm_scope TO idm_reader", "CREATE DOMAIN idm_scope.sample AS integer DEFAULT 7", "SET SESSION AUTHORIZATION idm_reader")
		denied := fixture.ControlsReferenceDiagnostic("42501", "must be owner of type idm_scope.sample")
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER DOMAIN idm_scope.sample SET DEFAULT 9", denied)
			s.Prepared, s.ErrorStage = prepared, "execute"
			c.run(s)
		}
		c.check("domain_schema='idm_scope'", idmInteger(c.database, "idm_scope", "sample", "7"))
		c.exec("RESET SESSION AUTHORIZATION", "REVOKE ALL ON DOMAIN idm_scope.sample FROM PUBLIC", "SET SESSION AUTHORIZATION idm_reader")
		c.check("domain_schema='idm_scope'")
		c.exec("RESET SESSION AUTHORIZATION")
		c.check("domain_schema='idm_scope'", idmInteger(c.database, "idm_scope", "sample", "7"))
	})
}
