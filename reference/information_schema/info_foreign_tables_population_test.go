package reference_test

import "testing"

func TestReferenceInfoForeignTablesPopulation(t *testing.T) {
	iftScenario(t, func(c *iftCase) {
		q := "SELECT " + iftProjection + " FROM information_schema.foreign_tables ORDER BY foreign_table_schema,foreign_table_name"
		c.routes(q, nil)
		c.exec("CREATE FOREIGN DATA WRAPPER ift_wrapper NO HANDLER NO VALIDATOR OPTIONS(wrapper_decoy 'wrapper-only')")
		c.routes(q, nil)
		c.exec("CREATE SERVER ift_server FOREIGN DATA WRAPPER ift_wrapper OPTIONS(server_decoy 'server-only')")
		c.routes(q, nil)
		c.exec("CREATE USER MAPPING FOR PUBLIC SERVER ift_server OPTIONS(password 'mapping-secret')")
		c.routes(q, nil)
		c.exec("CREATE SCHEMA ift_scope", "CREATE SCHEMA ift_moved", "CREATE FOREIGN TABLE ift_scope.ift_table(a integer OPTIONS(column_decoy 'column-only')) SERVER ift_server OPTIONS(remote 'before')")
		want := c.seedRows()
		c.routes(q, want)
		c.exec("CREATE FOREIGN TABLE ift_scope.ift_zero() SERVER ift_server", "CREATE FOREIGN TABLE ift_moved.ift_table(a integer) SERVER ift_server", "CREATE SERVER ift_second_server FOREIGN DATA WRAPPER ift_wrapper", "CREATE FOREIGN TABLE ift_scope.\"MiXeD_é🙂\"(a integer) SERVER ift_second_server", "CREATE TABLE ift_scope.ift_local(a integer)", "CREATE VIEW ift_scope.ift_view AS SELECT 1 AS a", "CREATE SEQUENCE ift_scope.ift_sequence")
		want = [][]any{c.row("ift_moved", "ift_table", "ift_server"), c.row("ift_scope", "MiXeD_é🙂", "ift_second_server"), c.row("ift_scope", "ift_table", "ift_server"), c.row("ift_scope", "ift_zero", "ift_server")}
		c.routes(q, want)
		for _, mutation := range []string{
			"ALTER FOREIGN DATA WRAPPER ift_wrapper OPTIONS(SET wrapper_decoy 'changed',ADD extra 'x')",
			"ALTER SERVER ift_server OPTIONS(SET server_decoy 'changed',ADD extra 'x')",
			"ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(SET remote 'changed',ADD password 'secret')",
			"ALTER FOREIGN TABLE ift_scope.ift_table ALTER COLUMN a OPTIONS(SET column_decoy 'changed',ADD extra 'x')",
			"ALTER USER MAPPING FOR PUBLIC SERVER ift_server OPTIONS(SET password 'changed',ADD extra 'x')",
			"ALTER FOREIGN DATA WRAPPER ift_wrapper OPTIONS(DROP extra)",
			"ALTER SERVER ift_server OPTIONS(DROP extra)",
			"ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(DROP password)",
			"ALTER FOREIGN TABLE ift_scope.ift_table ALTER COLUMN a OPTIONS(DROP extra)",
			"DROP USER MAPPING FOR PUBLIC SERVER ift_server",
		} {
			c.exec(mutation)
			c.routes(q, want)
		}
	})
}
