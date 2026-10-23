package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleColumnGrantsClasses(t *testing.T) {
	rcgScenario(t, func(c *rcgCase) {
		c.seed()
		c.t.Cleanup(func() {
			for _, q := range []string{"DROP FOREIGN TABLE IF EXISTS rcg_class_foreign", "DROP SERVER IF EXISTS rcg_server", "DROP FOREIGN DATA WRAPPER IF EXISTS rcg_fdw", "DROP FUNCTION IF EXISTS rcg_class_function()"} {
				if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
					c.t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		c.exec(
			"CREATE TABLE rcg_class_table(a integer,b integer GENERATED ALWAYS AS (a+1) STORED)",
			"CREATE VIEW rcg_class_view AS SELECT a FROM rcg_class_table",
			"CREATE FOREIGN DATA WRAPPER rcg_fdw NO HANDLER NO VALIDATOR",
			"CREATE SERVER rcg_server FOREIGN DATA WRAPPER rcg_fdw",
			"CREATE FOREIGN TABLE rcg_class_foreign(a integer) SERVER rcg_server",
			"CREATE TABLE rcg_class_parent(a integer) PARTITION BY RANGE(a)",
			"CREATE TABLE rcg_class_leaf PARTITION OF rcg_class_parent FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE rcg_class_zero()",
			"CREATE SEQUENCE rcg_class_sequence",
			"CREATE MATERIALIZED VIEW rcg_class_materialized AS SELECT a FROM rcg_class_table WITH NO DATA",
			"CREATE INDEX rcg_class_index ON rcg_class_table(a)",
			"CREATE TYPE rcg_class_type AS (a integer)",
			"CREATE FUNCTION rcg_class_function() RETURNS integer LANGUAGE SQL RETURN 1",
			"GRANT SELECT ON rcg_class_table,rcg_class_view,rcg_class_foreign,rcg_class_parent TO rcg_r",
			"GRANT SELECT ON rcg_class_sequence,rcg_class_materialized TO rcg_r",
			"GRANT USAGE ON TYPE rcg_class_type TO rcg_r",
			"GRANT EXECUTE ON FUNCTION rcg_class_function() TO rcg_r",
			"GRANT USAGE ON FOREIGN DATA WRAPPER rcg_fdw TO rcg_r",
			"GRANT USAGE ON FOREIGN SERVER rcg_server TO rcg_r",
		)
		var want [][]any
		for _, table := range []string{"rcg_class_table", "rcg_class_view", "rcg_class_foreign", "rcg_class_parent"} {
			want = append(want, c.row(c.owner, "rcg_r", "public", table, "a", "SELECT", "NO"))
			if table == "rcg_class_table" {
				want = append(want, c.row(c.owner, "rcg_r", "public", table, "b", "SELECT", "NO"))
			}
		}
		q := "SELECT * FROM information_schema.role_column_grants WHERE table_name LIKE 'rcg_class_%' AND grantee='rcg_r'"
		c.run(c.conn, c.step(q, want))
		c.run(c.conn, fixture.Prepared(c.step(q, want)))
		// Parent origins do not synthesize a leaf grant; the leaf has its own baseline.
		c.check(c.conn, "rcg_class_leaf", "rcg_r")
		c.check(c.conn, "rcg_class_leaf", c.owner, c.baseline(c.owner, "rcg_class_leaf", "a")...)
		c.check(c.conn, "rcg_class_zero", c.owner)
		for _, name := range []string{"rcg_class_sequence", "rcg_class_materialized", "rcg_class_index", "rcg_class_type", "rcg_class_function", "rcg_fdw", "rcg_server"} {
			c.check(c.conn, name, "rcg_r")
			c.check(c.conn, name, c.owner)
		}
		c.exec("GRANT UPDATE(a) ON rcg_class_leaf TO rcg_r")
		c.check(c.conn, "rcg_class_leaf", "rcg_r", c.row(c.owner, "rcg_r", "public", "rcg_class_leaf", "a", "UPDATE", "NO"))
	})
}
