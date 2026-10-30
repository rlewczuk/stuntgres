package reference_test

import "testing"

func TestReferenceInfoForeignTableOptionsPopulation(t *testing.T) {
	ftoScenario(t, func(c *ftoCase) {
		c.routes(ftoQuery, nil, "fto_table")
		c.seed()
		c.routes(ftoQuery, c.seedRows(), "fto_table")
		c.exec("CREATE FOREIGN TABLE fto_scope.fto_empty(a integer) SERVER fto_server", "CREATE TABLE fto_scope.fto_local(a integer)", "CREATE FOREIGN TABLE fto_scope.fto_zero() SERVER fto_server OPTIONS(password 'zero-secret')", "CREATE FOREIGN TABLE fto_scope.fto_second(a integer) SERVER fto_server OPTIONS(password 'second-secret')")
		c.routes(ftoQuery, nil, "fto_empty")
		c.routes(ftoQuery, nil, "fto_local")
		c.routes(ftoQuery, [][]any{c.row("fto_zero", "password", "zero-secret")}, "fto_zero")
		c.routes(ftoQuery, [][]any{c.row("fto_second", "password", "second-secret")}, "fto_second")
		c.exec("ALTER SERVER fto_server OPTIONS(SET server_decoy 'changed')", "ALTER FOREIGN DATA WRAPPER fto_wrapper OPTIONS(SET wrapper_decoy 'changed')", "ALTER FOREIGN TABLE fto_scope.fto_table ALTER COLUMN a OPTIONS(SET column_decoy 'changed')", "ALTER USER MAPPING FOR PUBLIC SERVER fto_server OPTIONS(SET mapping_decoy 'changed')")
		c.routes(ftoQuery, c.seedRows(), "fto_table")
		c.routes("SELECT "+ftoProjection+" FROM information_schema.foreign_table_options WHERE foreign_table_schema='fto_scope' AND option_name='password' ORDER BY foreign_table_name", [][]any{c.row("fto_second", "password", "second-secret"), c.row("fto_table", "password", "visible=value"), c.row("fto_zero", "password", "zero-secret")})
		c.exec("DROP SERVER fto_server CASCADE")
		c.routes(ftoQuery, nil, "fto_table")
		c.routes(ftoQuery, nil, "fto_zero")
	})
}
