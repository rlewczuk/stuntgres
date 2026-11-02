package reference_test

import "testing"

func TestReferenceInfoAttributesPopulation(t *testing.T) {
	attrScenario(t, func(c *attrCase) {
		c.exec("CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer,label varchar(7),amount numeric(8,2),tags integer[])")
		pair := [][]any{
			c.integer("pair", "id", 1),
			attrPatch(attrRow(c.database, "attr_scope", "pair", "label", 2, "character varying", "pg_catalog", "varchar"), map[string]any{"character_maximum_length": int64(7), "character_octet_length": int64(28)}),
			attrPatch(attrRow(c.database, "attr_scope", "pair", "amount", 3, "numeric", "pg_catalog", "numeric"), map[string]any{"numeric_precision": int64(8), "numeric_precision_radix": int64(10), "numeric_scale": int64(2)}),
			attrRow(c.database, "attr_scope", "pair", "tags", 4, "ARRAY", "pg_catalog", "_int4"),
		}
		where := "udt_schema='attr_scope' AND udt_name='pair'"
		c.check(where, pair...)
		c.held(c.hold(where), where, nil, pair...)
		c.exec("CREATE TYPE attr_scope.empty AS ()", "CREATE TYPE attr_scope.mood AS ENUM('ok')", `CREATE TYPE attr_scope."MiXeD雪" AS ("名" attr_scope.pair, mood attr_scope.mood, pairs attr_scope.pair[])`)
		quoted := [][]any{
			attrRow(c.database, "attr_scope", "MiXeD雪", "名", 1, "USER-DEFINED", "attr_scope", "pair"),
			attrRow(c.database, "attr_scope", "MiXeD雪", "mood", 2, "USER-DEFINED", "attr_scope", "mood"),
			attrRow(c.database, "attr_scope", "MiXeD雪", "pairs", 3, "ARRAY", "attr_scope", "_pair"),
		}
		c.check("udt_schema='attr_scope' AND udt_name='MiXeD雪'", quoted...)
		all := append(quoted, pair...)
		check := func() { c.check("udt_schema='attr_scope'", all...) }
		check()
		c.t.Cleanup(func() {
			c.exec("DROP SCHEMA IF EXISTS attr_scope CASCADE", "DROP SERVER IF EXISTS attr_server", "DROP FOREIGN DATA WRAPPER IF EXISTS attr_fdw")
		})
		for _, q := range []string{
			"CREATE TABLE attr_scope.table_row(v integer)", "CREATE TABLE attr_scope.zero_row()",
			"CREATE VIEW attr_scope.view_row AS SELECT v FROM attr_scope.table_row",
			"CREATE MATERIALIZED VIEW attr_scope.mat_row AS SELECT v FROM attr_scope.table_row WITH NO DATA",
			"CREATE TABLE attr_scope.parent(v integer) PARTITION BY RANGE(v)",
			"CREATE TABLE attr_scope.child PARTITION OF attr_scope.parent FOR VALUES FROM(0) TO(10)",
			"CREATE FOREIGN DATA WRAPPER attr_fdw NO HANDLER NO VALIDATOR", "CREATE SERVER attr_server FOREIGN DATA WRAPPER attr_fdw", "CREATE FOREIGN TABLE attr_scope.foreign_row(v integer) SERVER attr_server",
			"CREATE DOMAIN attr_scope.composite_domain AS attr_scope.pair",
			"CREATE TABLE attr_scope.arrays(v attr_scope.pair[],e integer[])",
		} {
			c.exec(q)
			check()
		}
		c.check("udt_schema='attr_scope' AND udt_name IN ('empty','_pair','_mood','composite_domain','table_row','view_row','mat_row','parent','child','arrays','record')")
		c.check("udt_schema='pg_catalog'")
	})
}
