package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceInfoForeignDataWrapperOptionsPopulation(t *testing.T) {
	fdwoScenario(t, func(c *fdwoCase) {
		c.routes(fdwoQuery, nil, "fdwo_wrapper")
		c.exec("CREATE FOREIGN DATA WRAPPER fdwo_empty NO HANDLER NO VALIDATOR")
		c.routes(fdwoQuery, nil, "fdwo_empty")
		c.seed()
		c.routes(fdwoQuery, c.seedRows(), "fdwo_wrapper")
		c.exec("CREATE SERVER fdwo_server FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(server_decoy 'server-only')", "CREATE SCHEMA fdwo_scope", "CREATE FOREIGN TABLE fdwo_scope.options(id integer OPTIONS(column_decoy 'column-only')) SERVER fdwo_server OPTIONS(table_decoy 'table-only')", "CREATE USER MAPPING FOR PUBLIC SERVER fdwo_server OPTIONS(mapping_decoy 'mapping-only')")
		c.routes(fdwoQuery, c.seedRows(), "fdwo_wrapper")
		c.exec("ALTER SERVER fdwo_server OPTIONS(SET server_decoy 'changed')", "ALTER FOREIGN TABLE fdwo_scope.options OPTIONS(SET table_decoy 'changed')", "ALTER FOREIGN TABLE fdwo_scope.options ALTER COLUMN id OPTIONS(SET column_decoy 'changed')", "ALTER USER MAPPING FOR PUBLIC SERVER fdwo_server OPTIONS(SET mapping_decoy 'changed')")
		c.routes(fdwoQuery, c.seedRows(), "fdwo_wrapper")
		long := strings.Repeat("é🙂", 100) + " = 'quoted' \\ trailing  "
		c.exec("CREATE FOREIGN DATA WRAPPER fdwo_second NO HANDLER NO VALIDATOR OPTIONS(empty '',password 'private=value',secret 'full-secret',token 'full-token',long '" + strings.ReplaceAll(long, "'", "''") + "')")
		second := [][]any{c.row("fdwo_second", "empty", ""), c.row("fdwo_second", "long", long), c.row("fdwo_second", "password", "private=value"), c.row("fdwo_second", "secret", "full-secret"), c.row("fdwo_second", "token", "full-token")}
		c.routes(fdwoQuery, second, "fdwo_second")
		c.routes(fdwoQuery, c.seedRows(), "fdwo_wrapper")
		c.routes("SELECT "+fdwoProjection+" FROM information_schema.foreign_data_wrapper_options WHERE foreign_data_wrapper_name='fdwo_wrapper' AND option_value=''", [][]any{c.row("fdwo_wrapper", "empty", "")})
		c.exec("DROP FOREIGN DATA WRAPPER fdwo_second", "DROP FOREIGN DATA WRAPPER fdwo_wrapper CASCADE")
		c.routes(fdwoQuery, nil, "fdwo_wrapper")
		c.routes(fdwoQuery, nil, "fdwo_second")
	})
}
