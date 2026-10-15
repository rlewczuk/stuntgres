package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWSecurity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("CREATE ROLE fdw_other", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_other SERVER fdw_server OPTIONS(secret 'hidden')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(secret 'shared')", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		d := fdwDiagnostic("42501", `permission denied to create foreign-data wrapper "fdw_denied"`)
		d.Hint = "Must be superuser to create a foreign-data wrapper."
		run(fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_denied", d))
		run(fixture.ErrorDiagnostic("SELECT * FROM pg_user_mapping", fdwDiagnostic("42501", "permission denied for table pg_user_mapping")))
		run(fixture.Select("SELECT usename,umoptions IS NULL AS redacted FROM pg_user_mappings WHERE srvname='fdw_server' ORDER BY usename", []string{"usename", "redacted"}, []string{"NAME", "BOOL"}, [][]any{{"fdw_other", true}, {"public", true}}))
		run(fixture.ErrorDiagnostic("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_server", fdwDiagnostic("42501", "permission denied for foreign server fdw_server")))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_server OPTIONS(secret 'own')", 0))
		run(fixture.Select("SELECT umoptions::text AS options FROM pg_user_mappings WHERE srvname='fdw_server' AND usename='fdw_reader'", []string{"options"}, []string{"TEXT"}, [][]any{{"{secret=own}"}}))
		run(fixture.ErrorDiagnostic("ALTER USER MAPPING FOR fdw_other SERVER fdw_server OPTIONS(SET secret 'stolen')", fdwDiagnostic("42501", "must be owner of foreign server fdw_server")))
		run(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceFDWMappingSelection(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE ROLE fdw_reader LOGIN PASSWORD 'fdw_reader_password'", 0))
		if !b.PG {
			fdwSourceExec(t, p, b, reader, run, "CREATE ROLE fdw_reader LOGIN PASSWORD 'fdw_reader_password'", 0)
		}
		fdwSourceExec(t, p, b, reader, run, "CREATE VIEW public.fdw_identity_source AS SELECT CASE WHEN current_user='fdw_reader' THEN 2 ELSE 1 END AS id", 0)
		fdwSourceExec(t, p, b, reader, run, "GRANT SELECT ON public.fdw_identity_source TO fdw_reader", 0)
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader", 0))
		publicOptions := "user 'stuntgres'"
		exactOptions := "user 'fdw_reader'"
		if b.PG {
			// The pinned loopback uses trust authentication. Only privileged
			// setup may waive postgres_fdw's non-superuser password-use policy.
			publicOptions += ",password 'stuntgres',password_required 'false'"
			exactOptions += ",password 'fdw_reader_password',password_required 'false'"
		}
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_live OPTIONS("+publicOptions+")", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_identity(id integer) SERVER fdw_live OPTIONS(schema_name 'public',table_name 'fdw_identity_source')", 0))
		run(fixture.Exec("GRANT SELECT ON fdw_identity TO fdw_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		} else {
			// Native schema SQL GRANT is unavailable. Transfer only the schema;
			// foreign relation ownership and explicit SELECT ACL stay unchanged.
			run(fixture.Exec("ALTER SCHEMA public OWNER TO fdw_reader", 0))
		}
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.Select("SELECT id FROM fdw_identity", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+exactOptions+")", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.Select("SELECT id FROM fdw_identity", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
		run(fixture.Select("SELECT id FROM fdw_identity", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceFDWImport(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.a(id integer)", 0)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.a VALUES(11)", 1)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.b(id integer)", 0)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.c(id integer)", 0)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.c VALUES(33)", 1)
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,c) FROM SERVER fdw_live INTO fdw_import", 0))
		run(fixture.Select("SELECT id FROM fdw_import.a UNION ALL SELECT id FROM fdw_import.c ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(33)}}))
		run(fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"a"}, {"c"}}))
		run(fixture.Exec("DROP FOREIGN TABLE fdw_import.a,fdw_import.c", 0))
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source EXCEPT(b,data) FROM SERVER fdw_live INTO fdw_import", 0))
		run(fixture.Select("SELECT id FROM fdw_import.a UNION ALL SELECT id FROM fdw_import.c ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(33)}}))
	})
}

func TestReferenceFDWImportAtomicity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.a(id integer)", 0)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.z(id integer)", 0)
		run(fixture.Exec("CREATE TABLE fdw_import.z(id integer)", 0))
		collision := fdwDiagnostic("42P07", `relation "z" already exists`)
		collision.Context = `importing foreign table "z"`
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", collision))
		run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relname='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("DROP TABLE fdw_import.z", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if b.PG {
			return
		}
		failure := errors.New("actual imported table validator rejected z")
		rejected := false
		reader.mu.Lock()
		reader.validateHook = func(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
			if kind == stuntgres.ForeignTable {
				for _, option := range options {
					if option.Name == "table_name" && option.Value == "z" {
						var visible int
						if err := p.Native.QueryRowContext(ctx, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'").Scan(&visible); err != nil {
							return err
						}
						if visible != 0 {
							return errors.New("import published an earlier table before all validations")
						}
						rejected = true
						return failure
					}
				}
			}
			return ctx.Err()
		}
		reader.mu.Unlock()
		defer func() { reader.mu.Lock(); reader.validateHook = nil; reader.mu.Unlock() }()
		before := reader.snapshot()
		fdwHostQueryError(t, p.Ctx, c, "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", failure)
		fdwReleased(t, reader, before, 0)
		if !rejected {
			t.Fatal("real later imported definition did not reach validator")
		}
		run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		failure = &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "SQL reader rejects imported z", Context: "provider validation"}
		validatorDiagnostic := fdwDiagnostic("22023", "SQL reader rejects imported z")
		validatorDiagnostic.Context = "provider validation\n" + `importing foreign table "z"`
		before = reader.snapshot()
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", validatorDiagnostic))
		fdwReleased(t, reader, before, 0)
		run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		reader.mu.Lock()
		reader.validateHook = nil
		reader.mu.Unlock()
		// The concrete reader rejects source types that its public codec metadata
		// cannot name. It must not invent a target type or a catalog fallback.
		fdwSourceExec(t, p, b, reader, run, "DROP TABLE fdw_source.z", 0)
		fdwSourceExec(t, p, b, reader, run, "CREATE TYPE fdw_source.fdw_enum AS ENUM('source')", 0)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.z(id fdw_source.fdw_enum)", 0)
		const unsupportedType = `SQL reader cannot import source type ""`
		before = reader.snapshot()
		_, err := c.ExecContext(p.Ctx, "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import")
		fixture.TransactionOrdinary(t, err, unsupportedType)
		fdwReleased(t, reader, before, 0)
		run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceFDWMissingSupport(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_no_handler(id integer) SERVER fdw_server OPTIONS(url 'https://not-a-provider.invalid')", 0))
		d := fdwDiagnostic("55000", `foreign-data wrapper "fdw_meta" has no handler`)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT * FROM fdw_no_handler", d)
			s.SelectRows = true
			s.Prepared = prepared
			run(s)
		}
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA absent FROM SERVER fdw_server INTO public", d))
	})
}
