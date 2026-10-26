package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTablePrivilegesClasses(t *testing.T) {
	tpScenario(t, func(c *tpCase) {
		c.seed()
		c.t.Cleanup(func() {
			for _, q := range []string{"DROP FOREIGN TABLE IF EXISTS tp_class_foreign", "DROP SERVER IF EXISTS tp_server", "DROP FOREIGN DATA WRAPPER IF EXISTS tp_fdw", "DROP FUNCTION IF EXISTS tp_class_function()"} {
				if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
					c.t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		c.exec(
			"CREATE TABLE tp_class_table(a integer,b integer GENERATED ALWAYS AS (a+1) STORED)",
			"CREATE VIEW tp_class_view AS SELECT a FROM tp_class_table",
			"CREATE FOREIGN DATA WRAPPER tp_fdw NO HANDLER NO VALIDATOR",
			"CREATE SERVER tp_server FOREIGN DATA WRAPPER tp_fdw",
			"CREATE FOREIGN TABLE tp_class_foreign(a integer) SERVER tp_server",
			"CREATE TABLE tp_class_parent(a integer) PARTITION BY RANGE(a)",
			"CREATE TABLE tp_class_leaf PARTITION OF tp_class_parent FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE tp_class_zero()",
			"CREATE SEQUENCE tp_class_sequence",
			"CREATE MATERIALIZED VIEW tp_class_materialized AS SELECT a FROM tp_class_table WITH NO DATA",
			"CREATE INDEX tp_class_index ON tp_class_table(a)",
			"CREATE TYPE tp_class_type AS (a integer)",
			"CREATE FUNCTION tp_class_function() RETURNS integer LANGUAGE SQL RETURN 1",
			"GRANT SELECT ON tp_class_table,tp_class_view,tp_class_foreign,tp_class_parent TO tp_r",
			"GRANT SELECT ON tp_class_sequence,tp_class_materialized TO tp_r",
			"GRANT USAGE ON TYPE tp_class_type TO tp_r",
			"GRANT EXECUTE ON FUNCTION tp_class_function() TO tp_r",
			"GRANT USAGE ON FOREIGN DATA WRAPPER tp_fdw TO tp_r",
			"GRANT USAGE ON FOREIGN SERVER tp_server TO tp_r",
		)
		var want [][]any
		for _, table := range []string{"tp_class_table", "tp_class_view", "tp_class_foreign", "tp_class_parent"} {
			want = append(want, c.row(c.owner, "tp_r", "public", table, "SELECT", "NO"))
		}
		q := "SELECT * FROM information_schema.table_privileges WHERE table_name LIKE 'tp_class_%' AND grantee='tp_r'"
		c.run(c.conn, c.step(q, want))
		c.run(c.conn, fixture.Prepared(c.step(q, want)))
		c.check(c.conn, "tp_class_leaf", "tp_r")
		c.check(c.conn, "tp_class_leaf", c.owner, c.baseline(c.owner, "tp_class_leaf")...)
		c.check(c.conn, "tp_class_zero", c.owner, c.baseline(c.owner, "tp_class_zero")...)
		for _, name := range []string{"tp_class_sequence", "tp_class_materialized", "tp_class_index", "tp_class_type", "tp_class_function", "tp_fdw", "tp_server", "tp_class_absent"} {
			c.check(c.conn, name, "tp_r")
			c.check(c.conn, name, c.owner)
		}
		c.exec("GRANT SELECT ON tp_class_leaf TO tp_r")
		c.check(c.conn, "tp_class_leaf", "tp_r", c.row(c.owner, "tp_r", "public", "tp_class_leaf", "SELECT", "NO"))
	})
}
