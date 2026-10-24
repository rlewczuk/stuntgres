package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleTableGrantsClasses(t *testing.T) {
	rtgScenario(t, func(c *rtgCase) {
		c.seed()
		c.t.Cleanup(func() {
			for _, q := range []string{"DROP FOREIGN TABLE IF EXISTS rtg_class_foreign", "DROP SERVER IF EXISTS rtg_server", "DROP FOREIGN DATA WRAPPER IF EXISTS rtg_fdw", "DROP FUNCTION IF EXISTS rtg_class_function()"} {
				if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
					c.t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		c.exec(
			"CREATE TABLE rtg_class_table(a integer,b integer GENERATED ALWAYS AS (a+1) STORED)",
			"CREATE VIEW rtg_class_view AS SELECT a FROM rtg_class_table",
			"CREATE FOREIGN DATA WRAPPER rtg_fdw NO HANDLER NO VALIDATOR",
			"CREATE SERVER rtg_server FOREIGN DATA WRAPPER rtg_fdw",
			"CREATE FOREIGN TABLE rtg_class_foreign(a integer) SERVER rtg_server",
			"CREATE TABLE rtg_class_parent(a integer) PARTITION BY RANGE(a)",
			"CREATE TABLE rtg_class_leaf PARTITION OF rtg_class_parent FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE rtg_class_zero()",
			"CREATE SEQUENCE rtg_class_sequence",
			"CREATE MATERIALIZED VIEW rtg_class_materialized AS SELECT a FROM rtg_class_table WITH NO DATA",
			"CREATE INDEX rtg_class_index ON rtg_class_table(a)",
			"CREATE TYPE rtg_class_type AS (a integer)",
			"CREATE FUNCTION rtg_class_function() RETURNS integer LANGUAGE SQL RETURN 1",
			"GRANT SELECT ON rtg_class_table,rtg_class_view,rtg_class_foreign,rtg_class_parent TO rtg_r",
			"GRANT SELECT ON rtg_class_sequence,rtg_class_materialized TO rtg_r",
			"GRANT USAGE ON TYPE rtg_class_type TO rtg_r",
			"GRANT EXECUTE ON FUNCTION rtg_class_function() TO rtg_r",
			"GRANT USAGE ON FOREIGN DATA WRAPPER rtg_fdw TO rtg_r",
			"GRANT USAGE ON FOREIGN SERVER rtg_server TO rtg_r",
		)
		var want [][]any
		for _, table := range []string{"rtg_class_table", "rtg_class_view", "rtg_class_foreign", "rtg_class_parent"} {
			want = append(want, c.row(c.owner, "rtg_r", "public", table, "SELECT", "NO"))
		}
		q := "SELECT * FROM information_schema.role_table_grants WHERE table_name LIKE 'rtg_class_%' AND grantee='rtg_r'"
		c.run(c.conn, c.step(q, want))
		c.run(c.conn, fixture.Prepared(c.step(q, want)))
		c.check(c.conn, "rtg_class_leaf", "rtg_r")
		c.check(c.conn, "rtg_class_leaf", c.owner, c.baseline(c.owner, "rtg_class_leaf")...)
		c.check(c.conn, "rtg_class_zero", c.owner, c.baseline(c.owner, "rtg_class_zero")...)
		for _, name := range []string{"rtg_class_sequence", "rtg_class_materialized", "rtg_class_index", "rtg_class_type", "rtg_class_function", "rtg_fdw", "rtg_server", "rtg_class_absent"} {
			c.check(c.conn, name, "rtg_r")
			c.check(c.conn, name, c.owner)
		}
		c.exec("GRANT SELECT ON rtg_class_leaf TO rtg_r")
		c.check(c.conn, "rtg_class_leaf", "rtg_r", c.row(c.owner, "rtg_r", "public", "rtg_class_leaf", "SELECT", "NO"))
	})
}
