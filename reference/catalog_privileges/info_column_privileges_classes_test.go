package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnPrivilegesClasses(t *testing.T) {
	t.Run("live_relation_and_attribute_kinds", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.t.Cleanup(func() {
				for _, query := range []string{"DROP FOREIGN TABLE IF EXISTS cp_class_foreign", "DROP SERVER IF EXISTS cp_server", "DROP FOREIGN DATA WRAPPER IF EXISTS cp_fdw"} {
					if _, err := c.conn.ExecContext(c.p.Ctx, query); err != nil {
						c.t.Error(query, err)
					}
				}
			})
			c.exec("CREATE TABLE cp_class_table(a integer,b integer GENERATED ALWAYS AS (a+1) STORED)",
				"CREATE VIEW cp_class_view AS SELECT a FROM cp_class_table",
				"CREATE FOREIGN DATA WRAPPER cp_fdw NO HANDLER NO VALIDATOR",
				"CREATE SERVER cp_server FOREIGN DATA WRAPPER cp_fdw",
				"CREATE FOREIGN TABLE cp_class_foreign(a integer) SERVER cp_server",
				"CREATE TABLE cp_class_parent(a integer) PARTITION BY RANGE(a)",
				"CREATE TABLE cp_class_leaf PARTITION OF cp_class_parent FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE cp_class_zero()", "CREATE SEQUENCE cp_class_sequence",
				"CREATE MATERIALIZED VIEW cp_class_materialized AS SELECT a FROM cp_class_table WITH NO DATA",
				"CREATE INDEX cp_class_index ON cp_class_table(a)", "CREATE TYPE cp_class_type AS (a integer)",
				"GRANT SELECT ON cp_class_table,cp_class_view,cp_class_foreign,cp_class_parent TO cp_r",
				"GRANT SELECT ON cp_class_sequence,cp_class_materialized TO cp_r", "GRANT USAGE ON TYPE cp_class_type TO cp_r")
			want := [][]any{
				c.row(c.owner, "cp_r", "public", "cp_class_table", "a", "SELECT", "NO"),
				c.row(c.owner, "cp_r", "public", "cp_class_table", "b", "SELECT", "NO"),
				c.row(c.owner, "cp_r", "public", "cp_class_view", "a", "SELECT", "NO"),
				c.row(c.owner, "cp_r", "public", "cp_class_foreign", "a", "SELECT", "NO"),
				c.row(c.owner, "cp_r", "public", "cp_class_parent", "a", "SELECT", "NO"),
			}
			query := "SELECT * FROM information_schema.column_privileges WHERE table_name LIKE 'cp_class_%' AND grantee='cp_r'"
			c.run(c.conn, c.step(query, want))
			c.run(c.conn, fixture.Prepared(c.step(query, want)))
			c.check(c.conn, "cp_class_leaf", "cp_r")
			c.check(c.conn, "cp_class_leaf", c.owner, c.baseline(c.owner, "cp_class_leaf", "a")...)
			c.check(c.conn, "cp_class_zero", c.owner)
			for _, name := range []string{"cp_class_sequence", "cp_class_materialized", "cp_class_index", "cp_class_type"} {
				c.check(c.conn, name, "cp_r")
				c.check(c.conn, name, c.owner)
			}
			c.exec("GRANT UPDATE(a) ON cp_class_leaf TO cp_r")
			c.check(c.conn, "cp_class_leaf", "cp_r", c.row(c.owner, "cp_r", "public", "cp_class_leaf", "a", "UPDATE", "NO"))
			c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO cp_r", "RESET ROLE", "ALTER TABLE cp_t DROP COLUMN a")
			c.check(c.conn, "cp_t", "cp_r")
			c.exec("ALTER TABLE cp_t ADD COLUMN a integer")
			c.check(c.conn, "cp_t", "cp_r")
			c.check(c.conn, "cp_absent", "cp_r")
		})
	})
	t.Run("schema_usage_is_not_visibility", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.exec("CREATE SCHEMA cp_scope AUTHORIZATION cp_o", "SET ROLE cp_o", "CREATE TABLE cp_scope.cp_hidden(a integer)", "GRANT SELECT(a) ON cp_scope.cp_hidden TO cp_p", "RESET ROLE")
			c.actor("cp_p", func() {
				c.check(c.conn, "cp_hidden", "cp_p", c.row("cp_o", "cp_p", "cp_scope", "cp_hidden", "a", "SELECT", "NO"))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT a FROM cp_scope.cp_hidden", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema cp_scope", Position: fixture.Position(15)})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			})
			c.actor("cp_r", func() { c.check(c.conn, "cp_hidden", "cp_p") })
		})
	})
	t.Run("other_session_temporary", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			peer := c.peer()
			for _, q := range []string{"SET ROLE cp_o", "CREATE TEMP TABLE cp_temp(a integer)", "GRANT SELECT(a) ON cp_temp TO cp_p"} {
				c.run(peer, fixture.Exec(q, 0))
			}
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='cp_temp'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			query := "SELECT * FROM information_schema.column_privileges WHERE table_schema=$1 AND table_name='cp_temp' AND grantee='cp_p'"
			c.actor("cp_p", func() {
				want := [][]any{c.row("cp_o", "cp_p", schema, "cp_temp", "a", "SELECT", "NO")}
				c.run(c.conn, c.step(query, want, schema))
				c.run(c.conn, fixture.Prepared(c.step(query, want, schema)))
			})
			c.run(peer, fixture.Exec("DROP TABLE cp_temp", 0))
		})
	})
}
