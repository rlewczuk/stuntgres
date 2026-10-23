package reference_test

import (
	"testing"
)

func TestReferenceInfoDomainsProjection(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, dataType, base string
		cells                             map[int]any
	}{
		{"smallint", "smallint", "smallint", "int2", map[int]any{12: int64(16), 13: int64(2), 14: int64(0)}},
		{"integer", "integer", "integer", "int4", map[int]any{12: int64(32), 13: int64(2), 14: int64(0)}},
		{"bigint", "bigint", "bigint", "int8", map[int]any{12: int64(64), 13: int64(2), 14: int64(0)}},
		{"real", "real", "real", "float4", map[int]any{12: int64(24), 13: int64(2)}},
		{"double", "double precision", "double precision", "float8", map[int]any{12: int64(53), 13: int64(2)}},
		{"numeric", "numeric", "numeric", "numeric", map[int]any{13: int64(10)}},
		{"numeric_fixed", "numeric(9,2)", "numeric", "numeric", map[int]any{12: int64(9), 13: int64(10), 14: int64(2)}},
		// Released _pg_numeric_scale masks the encoded scale, not its signed value.
		{"numeric_negative", "numeric(4,-2)", "numeric", "numeric", map[int]any{12: int64(4), 13: int64(10), 14: int64(2046)}},
		{"varchar_fixed", "varchar(5)", "character varying", "varchar", map[int]any{4: int64(5), 5: int64(20)}},
		{"varchar", "varchar", "character varying", "varchar", map[int]any{5: int64(1073741824)}},
		{"text", "text", "text", "text", map[int]any{5: int64(1073741824)}},
		{"char", "char(3)", "character", "bpchar", map[int]any{4: int64(3), 5: int64(12)}},
		{"bit", "bit(3)", "bit", "bit", map[int]any{4: int64(3)}},
		{"varbit", "varbit(4)", "bit varying", "varbit", map[int]any{4: int64(4)}},
		{"date", "date", "date", "date", map[int]any{15: int64(0)}},
		{"time", "time(2)", "time without time zone", "time", map[int]any{15: int64(2)}},
		{"timetz", "time with time zone", "time with time zone", "timetz", map[int]any{15: int64(6)}},
		{"timestamp", "timestamp", "timestamp without time zone", "timestamp", map[int]any{15: int64(6)}},
		{"timestamptz", "timestamp(3) with time zone", "timestamp with time zone", "timestamptz", map[int]any{15: int64(3)}},
		{"interval", "interval", "interval", "interval", map[int]any{15: int64(6)}},
		{"interval_fields", "interval day to second(2)", "interval", "interval", map[int]any{15: int64(2), 16: "DAY TO SECOND(2)"}},
		// Released domains CASE checks the domain typelem, not the array base.
		{"array", "integer[]", "integer[]", "_int4", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idmScenario(t, func(c *idmCase) {
				c.exec("CREATE SCHEMA idm_scope", "CREATE DOMAIN idm_scope.sample AS "+tc.declaration)
				c.check("domain_schema='idm_scope'", idmRow(c.database, "idm_scope", "sample", tc.dataType, "pg_catalog", tc.base, tc.cells))
			})
		})
	}
	for _, collation := range []string{"default", "C", "POSIX"} {
		t.Run("collation_"+collation, func(t *testing.T) {
			idmScenario(t, func(c *idmCase) {
				c.exec("CREATE SCHEMA idm_scope", `CREATE DOMAIN idm_scope.sample AS text COLLATE "`+collation+`"`)
				cells := map[int]any{5: int64(1073741824)}
				if collation != "default" {
					cells[9], cells[10], cells[11] = c.database, "pg_catalog", collation
				}
				c.check("domain_schema='idm_scope'", idmRow(c.database, "idm_scope", "sample", "text", "pg_catalog", "text", cells))
			})
		})
	}
	for _, family := range []struct{ name, setup, base string }{
		{"enum", `CREATE TYPE idm_scope.base AS ENUM ('a','b')`, "base"},
		{"composite", `CREATE TYPE idm_scope.base AS (v integer)`, "base"},
		{"range", "", "int4range"},
		{"extension", "CREATE EXTENSION intarray WITH SCHEMA idm_scope", "query_int"},
	} {
		t.Run(family.name, func(t *testing.T) {
			idmScenario(t, func(c *idmCase) {
				c.exec("CREATE SCHEMA idm_scope")
				if family.setup != "" {
					c.exec(family.setup)
				}
				baseSchema := "idm_scope"
				if family.name == "range" {
					baseSchema = "pg_catalog"
				}
				c.exec("CREATE DOMAIN idm_scope.sample AS " + baseSchema + "." + family.base)
				dataType := "USER-DEFINED"
				if family.name == "range" {
					dataType = "int4range"
				}
				c.check("domain_schema='idm_scope'", idmRow(c.database, "idm_scope", "sample", dataType, baseSchema, family.base, nil))
			})
		})
	}
}

func TestReferenceInfoDomainsSearchPath(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		c.exec(
			"CREATE SCHEMA idm_scope",
			"CREATE TYPE idm_scope.int4range AS ENUM ('x')",
			"CREATE TYPE idm_scope.text AS ENUM ('x')",
			"CREATE DOMAIN idm_scope.sample AS pg_catalog.int4range",
			"CREATE DOMAIN idm_scope.array_sample AS pg_catalog.text[]",
			"SET search_path = idm_scope, pg_catalog",
		)
		const where = "domain_schema='idm_scope'"
		held := c.hold(where)
		for _, phase := range []struct {
			path, rangeName, arrayName string
		}{
			{"idm_scope, pg_catalog", "pg_catalog.int4range", "pg_catalog.text[]"},
			{"pg_catalog, idm_scope", "int4range", "text[]"},
			{"idm_scope, pg_catalog", "pg_catalog.int4range", "pg_catalog.text[]"},
		} {
			c.exec("SET search_path = " + phase.path)
			// Search path changes only the default-case format_type spelling:
			// both domain rows and their immediate base identities stay intact.
			// TEXT[] witnesses qualification of the element, not of _text.
			c.held(held, where, nil,
				idmRow(c.database, "idm_scope", "sample", phase.rangeName, "pg_catalog", "int4range", nil),
				idmRow(c.database, "idm_scope", "array_sample", phase.arrayName, "pg_catalog", "_text", nil),
			)
		}
	})
}
