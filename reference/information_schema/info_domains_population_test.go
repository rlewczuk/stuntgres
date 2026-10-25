package reference_test

import "testing"

func TestReferenceInfoDomainsPopulation(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		c.check("domain_schema='information_schema' AND domain_name IN ('cardinal_number','character_data','sql_identifier','yes_or_no','time_stamp')",
			idmInteger(c.database, "information_schema", "cardinal_number", nil),
			idmRow(c.database, "information_schema", "character_data", "character varying", "pg_catalog", "varchar", map[int]any{5: int64(1073741824), 9: c.database, 10: "pg_catalog", 11: "C"}),
			idmRow(c.database, "information_schema", "sql_identifier", "name", "pg_catalog", "name", map[int]any{9: c.database, 10: "pg_catalog", 11: "C"}),
			idmRow(c.database, "information_schema", "yes_or_no", "character varying", "pg_catalog", "varchar", map[int]any{4: int64(3), 5: int64(12), 9: c.database, 10: "pg_catalog", 11: "C"}),
			idmRow(c.database, "information_schema", "time_stamp", "timestamp with time zone", "pg_catalog", "timestamptz", map[int]any{15: int64(2), 18: "CURRENT_TIMESTAMP(2)"}),
		)
		c.exec("CREATE SCHEMA idm_scope",
			"CREATE DOMAIN idm_scope.plain AS integer",
			"CREATE DOMAIN idm_scope.checked AS integer CHECK(VALUE>0)",
			"CREATE DOMAIN idm_scope.nn AS integer NOT NULL",
			"CREATE DOMAIN idm_scope.child AS idm_scope.checked",
			"CREATE TYPE idm_scope.ordinary AS ENUM ('a')",
			"CREATE TYPE idm_scope.composite AS (v integer)",
			"CREATE TABLE idm_scope.t(v integer)")
		where := "domain_schema='idm_scope'"
		h := c.hold(where)
		c.held(h, where, nil,
			idmInteger(c.database, "idm_scope", "plain", nil),
			idmInteger(c.database, "idm_scope", "checked", nil),
			idmInteger(c.database, "idm_scope", "nn", nil),
			idmRow(c.database, "idm_scope", "child", "USER-DEFINED", "idm_scope", "checked", nil))
	})
}

func TestReferenceInfoDomainsInheritance(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		c.exec("CREATE SCHEMA idm_scope",
			`CREATE DOMAIN idm_scope.parent AS varchar(5) COLLATE "C" DEFAULT 'abc' NOT NULL CHECK(VALUE<>'')`,
			"CREATE DOMAIN idm_scope.child AS idm_scope.parent",
			"CREATE DOMAIN idm_scope.ip AS integer DEFAULT 7",
			"CREATE DOMAIN idm_scope.ic AS idm_scope.ip")
		parent := idmVarchar(c.database, "idm_scope", "parent", "'abc'::character varying")
		child := idmRow(c.database, "idm_scope", "child", "USER-DEFINED", "idm_scope", "parent", map[int]any{9: c.database, 10: "pg_catalog", 11: "C", 18: "'abc'::character varying"})
		ip := idmInteger(c.database, "idm_scope", "ip", "7")
		ic := idmRow(c.database, "idm_scope", "ic", "USER-DEFINED", "idm_scope", "ip", map[int]any{18: "7"})
		where := "domain_schema='idm_scope'"
		h := c.hold(where)
		c.held(h, where, nil, parent, child, ip, ic)
		c.exec("ALTER DOMAIN idm_scope.ip SET DEFAULT 11", "CREATE DOMAIN idm_scope.late AS idm_scope.ip")
		ip[18] = "11"
		late := idmRow(c.database, "idm_scope", "late", "USER-DEFINED", "idm_scope", "ip", map[int]any{18: "11"})
		c.held(h, where, nil, parent, child, ip, ic, late)
		c.exec("ALTER DOMAIN idm_scope.ic DROP DEFAULT")
		ic[18] = nil
		c.held(h, where, nil, parent, child, ip, ic, late)
		c.exec("ALTER DOMAIN idm_scope.ic SET DEFAULT NULL")
		// Candidate from stored cooked NULL; pinned oracle resolves presence/text.
		ic[18] = "NULL::integer"
		c.held(h, where, nil, parent, child, ip, ic, late)
	})
}
