package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceInfoForeignServerOptionsPopulation(t *testing.T) {
	fsopScenario(t, func(c *fsopCase) {
		c.routes(fsopQuery, nil, "fsop_server")
		c.seed()
		c.routes(fsopQuery, c.seedRows(), "fsop_server")
		c.routes(fsopQuery, nil, "fsop_wrapper")
		c.exec("CREATE SERVER fsop_empty FOREIGN DATA WRAPPER fsop_wrapper", "CREATE SCHEMA fsop_scope", "CREATE FOREIGN TABLE fsop_scope.options(id integer OPTIONS(column_decoy 'column-only')) SERVER fsop_server OPTIONS(table_decoy 'table-only')", "CREATE USER MAPPING FOR PUBLIC SERVER fsop_server OPTIONS(mapping_decoy 'mapping-only')")
		c.routes(fsopQuery, nil, "fsop_empty")
		c.exec("ALTER SERVER fsop_empty VERSION 'still-optionless'")
		c.routes(fsopQuery, nil, "fsop_empty")
		c.exec("ALTER FOREIGN DATA WRAPPER fsop_wrapper OPTIONS(SET wrapper_only 'changed')", "ALTER FOREIGN TABLE fsop_scope.options OPTIONS(SET table_decoy 'changed')", "ALTER FOREIGN TABLE fsop_scope.options ALTER COLUMN id OPTIONS(SET column_decoy 'changed')", "ALTER USER MAPPING FOR PUBLIC SERVER fsop_server OPTIONS(SET mapping_decoy 'changed')")
		c.routes(fsopQuery, c.seedRows(), "fsop_server")
		long := strings.Repeat("é🙂", 100) + " = 'quoted' \\ trailing  "
		c.exec("CREATE SERVER fsop_second TYPE 'kind' VERSION 'one' FOREIGN DATA WRAPPER fsop_wrapper OPTIONS(empty '',host 'example',password 'private=value',secret 'full-secret',token 'é=🙂',long '" + strings.ReplaceAll(long, "'", "''") + "')")
		second := [][]any{c.row("fsop_second", "empty", ""), c.row("fsop_second", "host", "example"), c.row("fsop_second", "long", long), c.row("fsop_second", "password", "private=value"), c.row("fsop_second", "secret", "full-secret"), c.row("fsop_second", "token", "é=🙂")}
		c.routes(fsopQuery, second, "fsop_second")
		c.routes("SELECT "+fsopProjection+" FROM information_schema.foreign_server_options WHERE foreign_server_name IN ('fsop_server','fsop_second') ORDER BY foreign_server_name,option_name", append(second, c.seedRows()...))
		c.exec("ALTER SERVER fsop_second VERSION 'two'")
		c.routes(fsopQuery, second, "fsop_second")
		c.routes("SELECT "+fsopProjection+" FROM information_schema.foreign_server_options WHERE foreign_server_name='fsop_server' AND option_value=''", [][]any{c.row("fsop_server", "empty", "")})
		c.exec("ALTER SERVER fsop_empty OPTIONS(ADD only 'one')")
		c.routes(fsopQuery, [][]any{c.row("fsop_empty", "only", "one")}, "fsop_empty")
		c.exec("ALTER SERVER fsop_empty OPTIONS(DROP only)")
		c.routes(fsopQuery, nil, "fsop_empty")
		c.exec("DROP SERVER fsop_second", "DROP SERVER fsop_server CASCADE")
		c.routes(fsopQuery, nil, "fsop_server")
		c.routes(fsopQuery, nil, "fsop_second")
	})
}
