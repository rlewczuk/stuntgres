package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWSecurity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("CREATE ROLE fdw_other", 0))
		run(fixture.Exec("CREATE ROLE fdw_owner", 0))
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
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, code, message string }{
				{"ALTER USER MAPPING FOR fdw_owner SERVER fdw_server OPTIONS(secret 'absent')", "42704", `user mapping for "fdw_owner" does not exist for server "fdw_server"`},
				{"DROP USER MAPPING FOR fdw_owner SERVER fdw_server", "42704", `user mapping for "fdw_owner" does not exist for server "fdw_server"`},
				{"CREATE USER MAPPING FOR fdw_owner SERVER fdw_server", "42501", "must be owner of foreign server fdw_server"},
				{"DROP USER MAPPING FOR fdw_other SERVER fdw_server", "42501", "must be owner of foreign server fdw_server"},
			} {
				step := fixture.ErrorDiagnostic(tc.q, fdwDiagnostic(tc.code, tc.message))
				step.Prepared = prepared
				run(step)
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT umoptions::text AS options FROM pg_user_mappings WHERE srvname='fdw_server' AND usename='fdw_other'", []string{"options"}, []string{"TEXT"}, [][]any{{"{secret=hidden}"}}))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET ROLE fdw_reader", 0))
		for _, prepared := range []bool{false, true} {
			notices = nil
			step := fixture.Exec("DROP USER MAPPING IF EXISTS FOR fdw_owner SERVER fdw_server", 0)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, nc, recorder, step)
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `user mapping for "fdw_owner" does not exist for server "fdw_server", skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("missing mapping notices got %#v want %#v", notices, want)
			}
		}
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET ROLE", 0))
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
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
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
	setup := func(t *testing.T, p *fixture.Pair, b fixture.Backend, reader *fdwSQLReader, run func(fixture.Step)) {
		t.Helper()
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.a(id integer)", 0)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.z(id integer)", 0)
	}
	t.Run("collision-and-rollback", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
			setup(t, p, b, reader, run)
			run(fixture.Exec("CREATE TABLE fdw_import.z(id integer)", 0))
			collision := fdwDiagnostic("42P07", `relation "z" already exists`)
			collision.Context = `importing foreign table "z"`
			run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", collision))
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relname='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, false)
			run(fixture.Exec("DROP TABLE fdw_import.z", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, false)
		})
	})
	t.Run("later-validator", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
			setup(t, p, b, reader, run)
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, false)
			if b.PG {
				return
			}
			sentinel := errors.New("actual imported table validator rejected z")
			var failure error = sentinel
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
			ordinary := fdwHostQueryError(t, p.Ctx, c, "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", failure)
			fixture.TransactionOrdinary(t, ordinary, sentinel.Error())
			fdwReleased(t, reader, before, 0)
			if !rejected {
				t.Fatal("real later imported definition did not reach validator")
			}
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, false)
			typed := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "SQL reader rejects imported z", Context: "provider validation"}
			failure = typed
			rejected = false
			validatorDiagnostic := fdwDiagnostic("22023", "SQL reader rejects imported z")
			validatorDiagnostic.Context = "provider validation\n" + `importing foreign table "z"`
			before = reader.snapshot()
			run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import", validatorDiagnostic))
			fdwReleased(t, reader, before, 0)
			if !rejected {
				t.Fatal("typed later imported definition did not reach validator")
			}
			if typed.Context != "provider validation" || sentinel.Error() != "actual imported table validator rejected z" {
				t.Fatal("import mutated the original validator error")
			}
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, false)
			reader.mu.Lock()
			reader.validateHook = nil
			reader.mu.Unlock()
		})
	})
	t.Run("unsupported-source", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
			setup(t, p, b, reader, run)
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, false)
			if b.PG {
				return
			}
			// Keep the genuine integer-to-enum source transition. The reader
			// cannot name this source codec and must not invent a target type.
			fdwSourceExec(t, p, b, reader, run, "DROP TABLE fdw_source.z", 0)
			fdwSourceExec(t, p, b, reader, run, "CREATE TYPE fdw_source.fdw_enum AS ENUM('source')", 0)
			fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.z(id fdw_source.fdw_enum)", 0)
			const unsupportedType = `SQL reader cannot import source type ""`
			before := reader.snapshot()
			result, err := c.ExecContext(p.Ctx, "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(a,z) FROM SERVER fdw_live INTO fdw_import")
			if result != nil {
				t.Fatal("failed unsupported-source import published a Result")
			}
			fixture.TransactionOrdinary(t, err, unsupportedType)
			fdwReleased(t, reader, before, 0)
			run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fdwImportSourceSurvives(t, p, b, reader, run, true)
		})
	})
}

func fdwImportSourceSurvives(t *testing.T, p *fixture.Pair, b fixture.Backend, reader *fdwSQLReader, run func(fixture.Step), enum bool) {
	t.Helper()
	zType, zSchema := "int4", "pg_catalog"
	if enum {
		zType, zSchema = "fdw_enum", "fdw_source"
	}
	steps := []fixture.Step{
		fixture.Select("SELECT c.relname,a.attname,t.typname,tn.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace WHERE n.nspname='fdw_source' AND c.relname IN ('a','z') AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum", []string{"relname", "attname", "typname", "nspname"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{"a", "id", "int4", "pg_catalog"}, {"z", "id", zType, zSchema}}),
		fixture.Select("SELECT id FROM fdw_source.a", []string{"id"}, []string{"INT4"}, nil),
		fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}),
	}
	if !enum {
		steps = append(steps, fixture.Select("SELECT id FROM fdw_source.z", []string{"id"}, []string{"INT4"}, nil))
	} else {
		// The actual public codec name is empty, not an invented enum name.
		steps = append(steps, fixture.Select("SELECT id FROM fdw_source.z", []string{"id"}, []string{""}, nil))
	}
	for _, step := range steps {
		if b.PG {
			run(step)
			continue
		}
		fdwImportSourceRows(t, p.Ctx, reader, step)
	}
}

// The independent native source has no recording middleware. Inspect its actual
// public Rows directly under the existing work context; target reads still use
// the original full RunStep/raw/metadata/middleware route.
func fdwImportSourceRows(t *testing.T, ctx context.Context, reader *fdwSQLReader, step fixture.Step) {
	t.Helper()
	rows, err := reader.source.QueryContext(ctx, step.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, step.Columns) {
		t.Fatalf("source columns=%v error=%v want=%v", columns, err, step.Columns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	actualTypes := make([]string, len(types))
	for i, typ := range types {
		actualTypes[i] = typ.DatabaseTypeName()
	}
	if !reflect.DeepEqual(actualTypes, step.Types) {
		t.Fatalf("source types=%v want=%v", actualTypes, step.Types)
	}
	var values [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, step.Rows) {
		t.Fatalf("source rows=%#v want=%#v", values, step.Rows)
	}
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
			for _, q := range []string{"INSERT INTO fdw_no_handler VALUES(1)", "UPDATE fdw_no_handler SET id=2", "DELETE FROM fdw_no_handler"} {
				write := fixture.ErrorDiagnostic(q, d)
				write.Prepared = prepared
				run(write)
			}
		}
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA absent FROM SERVER fdw_server INTO public", d))
	})
}
