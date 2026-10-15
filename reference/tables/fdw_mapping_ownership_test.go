package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWMappingOwnership(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		fdwMetadata(run)
		for _, role := range []string{"fdw_reader", "fdw_other", "fdw_owner"} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO fdw_owner", 0))
		run(fixture.Exec("ALTER SERVER fdw_server OWNER TO fdw_owner", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_server OPTIONS(tag 'old')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_other SERVER fdw_server OPTIONS(tag 'recipient')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(tag 'public')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_owned(id integer) SERVER fdw_server", 0))
		run(fixture.Exec("CREATE TABLE fdw_mapping_before(mapping_oid oid,role_oid oid,server_oid oid,options text)", 0))
		run(fixture.Exec("INSERT INTO fdw_mapping_before SELECT oid,umuser,umserver,umoptions::text FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_server')", 3))
		unchanged := fixture.Select("SELECT m.oid=b.mapping_oid AS same_oid,m.umserver=b.server_oid AS same_server,m.umoptions::text=b.options AS same_options FROM pg_user_mapping m JOIN fdw_mapping_before b ON m.umuser=b.role_oid AND m.umserver=b.server_oid ORDER BY m.umuser", []string{"same_oid", "same_server", "same_options"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {true, true, true}, {true, true, true}})
		blocked := fdwDiagnostic("2BP01", `role "fdw_reader" cannot be dropped because some objects depend on it`)
		blocked.Detail = "owner of user mapping for fdw_reader on server fdw_server"
		run(fixture.ErrorDiagnostic("DROP ROLE fdw_reader", blocked))
		run(fixture.Exec("REASSIGN OWNED BY fdw_reader TO fdw_other", 0))
		run(unchanged)
		run(fixture.ErrorDiagnostic("DROP ROLE fdw_reader", blocked))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_mapping_drop", 0))
		run(fixture.Exec("DROP USER MAPPING FOR fdw_reader SERVER fdw_server", 0))
		run(fixture.Select("SELECT count(*) FROM pg_user_mapping WHERE umuser='fdw_reader'::regrole::oid AND umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_server')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("ROLLBACK TO before_mapping_drop", 0))
		run(unchanged)
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("DROP OWNED BY fdw_other", 0))
		options := func(want [][]any) fixture.Step {
			return fixture.Select("SELECT umoptions::text AS options FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_server') ORDER BY umoptions::text", []string{"options"}, []string{"TEXT"}, want)
		}
		run(options([][]any{{"{tag=old}"}, {"{tag=public}"}}))
		run(fixture.ErrorDiagnostic("DROP ROLE fdw_reader", blocked))
		run(fixture.Exec("DROP OWNED BY fdw_reader", 0))
		run(options([][]any{{"{tag=public}"}}))
		run(fixture.Exec("DROP ROLE fdw_reader", 0))
		run(fixture.Select("SELECT w.fdwname,s.srvname,c.relkind AS kind FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid JOIN pg_foreign_table f ON f.ftserver=s.oid JOIN pg_class c ON c.oid=f.ftrelid WHERE c.relname='fdw_owned'", []string{"fdwname", "srvname", "kind"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"fdw_meta", "fdw_server", "f"}}))
		run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
		run(fixture.Exec("DROP FOREIGN TABLE fdw_owned", 0))
		run(fixture.ErrorDiagnostic("DROP SERVER fdw_server", fixture.PgDependRestrict("server fdw_server", "user mapping for public on server fdw_server depends on server fdw_server")))
		run(fixture.Exec("DROP SERVER fdw_server CASCADE", 0))
		run(fixture.Select("SELECT count(*) FROM pg_user_mapping m JOIN fdw_mapping_before b ON m.oid=b.mapping_oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
