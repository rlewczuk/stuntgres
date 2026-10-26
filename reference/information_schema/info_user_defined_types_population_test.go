package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUserDefinedTypesPopulation(t *testing.T) {
	udtScenario(t, func(c *udtCase) {
		c.exec("CREATE SCHEMA udt_scope", "CREATE TYPE udt_scope.alpha AS (v integer)", "CREATE TYPE udt_scope.empty AS ()", `CREATE TYPE udt_scope."MiXeD雪" AS (v varchar(7), a integer[])`)
		want := [][]any{udtRow(c.database, "udt_scope", "alpha"), udtRow(c.database, "udt_scope", "empty"), udtRow(c.database, "udt_scope", "MiXeD雪")}
		check := func() { c.check("user_defined_type_schema='udt_scope'", want...) }
		check()
		c.t.Cleanup(func() {
			c.exec("DROP SCHEMA IF EXISTS udt_scope CASCADE", "DROP SERVER IF EXISTS udt_server", "DROP FOREIGN DATA WRAPPER IF EXISTS udt_fdw")
		})
		for _, q := range []string{
			"CREATE TABLE udt_scope.table_row(v integer)", "CREATE TABLE udt_scope.zero_row()",
			"CREATE VIEW udt_scope.view_row AS SELECT v FROM udt_scope.table_row",
			"CREATE MATERIALIZED VIEW udt_scope.mat_row AS SELECT v FROM udt_scope.table_row WITH NO DATA",
			"CREATE TABLE udt_scope.parent(v integer) PARTITION BY RANGE(v)",
			"CREATE TABLE udt_scope.child PARTITION OF udt_scope.parent FOR VALUES FROM(0) TO(10)",
			"CREATE FOREIGN DATA WRAPPER udt_fdw NO HANDLER NO VALIDATOR", "CREATE SERVER udt_server FOREIGN DATA WRAPPER udt_fdw", "CREATE FOREIGN TABLE udt_scope.foreign_row(v integer) SERVER udt_server",
			"CREATE TYPE udt_scope.enum_negative AS ENUM('a')", "CREATE DOMAIN udt_scope.domain_negative AS integer", "CREATE DOMAIN udt_scope.composite_domain AS udt_scope.alpha",
			"CREATE TYPE udt_scope.range_negative AS RANGE(subtype=integer,multirange_type_name=multirange_negative)",
			"CREATE TABLE udt_scope.arrays(v udt_scope.alpha[],e integer[],d udt_scope.domain_negative[],r udt_scope.range_negative[])",
		} {
			c.exec(q)
			check()
		}
		// Companion descriptors exist independently of executable array-column admission.
		s := fixture.Select(`SELECT t.typname,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,a.typnamespace=t.typnamespace AS namespace_link FROM pg_catalog.pg_type t JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE t.typnamespace='udt_scope'::regnamespace AND t.typname IN ('alpha','domain_negative','enum_negative','range_negative') ORDER BY t.typname`, []string{"typname", "element_link", "array_link", "namespace_link"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"alpha", true, true, true}, {"domain_negative", true, true, true}, {"enum_negative", true, true, true}, {"range_negative", true, true, true}})
		c.run(s)
		c.run(fixture.Prepared(s))
		check()
		c.check("user_defined_type_schema='udt_scope' AND user_defined_type_name IN ('_alpha','_domain_negative','_enum_negative','_range_negative','multirange_negative','_multirange_negative')")
		c.check("user_defined_type_schema='pg_catalog'")
	})
}
