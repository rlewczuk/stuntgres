package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWMetadataLifecycle(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(secret 'public')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_table(id integer OPTIONS(remote 'key'),label text) SERVER fdw_server OPTIONS(remote 'data')", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT fdwname,fdwhandler=0 AS no_handler,fdwvalidator=0 AS no_validator,fdwoptions IS NULL AS no_options,fdwacl IS NULL AS no_acl FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'", []string{"fdwname", "no_handler", "no_validator", "no_options", "no_acl"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"fdw_meta", true, true, true, true}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT s.srvname,w.fdwname,s.srvtype IS NULL AS no_type,s.srvversion IS NULL AS no_version FROM pg_foreign_server s JOIN pg_foreign_data_wrapper w ON w.oid=s.srvfdw WHERE s.srvname='fdw_server'", []string{"srvname", "fdwname", "no_type", "no_version"}, []string{"NAME", "NAME", "BOOL", "BOOL"}, [][]any{{"fdw_server", "fdw_meta", true, true}}))
		run(fixture.Select("SELECT c.relkind AS kind,s.srvname,f.ftoptions::text AS options FROM pg_foreign_table f JOIN pg_class c ON c.oid=f.ftrelid JOIN pg_foreign_server s ON s.oid=f.ftserver WHERE c.relname='fdw_table'", []string{"kind", "srvname", "options"}, []string{"CHAR", "NAME", "TEXT"}, [][]any{{"f", "fdw_server", "{remote=data}"}}))
		run(fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_table'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{remote=key}"}, {"label", nil}}))
		run(fixture.Select("SELECT umuser=0 AS public_mapping,umoptions::text AS options FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_server')", []string{"public_mapping", "options"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{secret=public}"}}))
		run(fixture.Exec("ALTER SERVER fdw_server VERSION ''", 0))
		run(fixture.Select("SELECT srvversion FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"srvversion"}, []string{"TEXT"}, [][]any{{""}}))
		run(fixture.Exec("ALTER SERVER fdw_server VERSION NULL", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_table RENAME COLUMN label TO note", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_table RENAME TO fdw_renamed", 0))
		run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_renamed SET SCHEMA fdw_import", 0))
		run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='fdw_import.fdw_renamed'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"id"}, {"note"}}))
		run(fixture.Exec("DROP FOREIGN TABLE fdw_import.fdw_renamed", 0))
		run(fixture.Exec("DROP USER MAPPING FOR PUBLIC SERVER fdw_server", 0))
		run(fixture.Exec("DROP SERVER fdw_server", 0))
		run(fixture.Exec("DROP FOREIGN DATA WRAPPER fdw_meta", 0))
	})
}

func TestReferenceFDWOptionsAtomicity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("ALTER SERVER fdw_server OPTIONS(a '1',b '2')", 0))
		run(fixture.Exec("ALTER SERVER fdw_server OPTIONS(SET a '3',DROP b,ADD c '4')", 0))
		want := fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"options"}, []string{"TEXT"}, [][]any{{"{a=3,c=4}"}})
		run(want)
		run(fixture.ErrorDiagnostic("ALTER SERVER fdw_server OPTIONS(SET a 'wrong',ADD c 'duplicate')", fdwDiagnostic("42710", `option "c" provided more than once`)))
		run(want)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_options(id integer OPTIONS(a 'column')) SERVER fdw_server OPTIONS(a 'table')", 0))
		run(fixture.ErrorDiagnostic("ALTER FOREIGN TABLE fdw_options OPTIONS(SET a 'wrong'), ALTER COLUMN id OPTIONS(DROP absent)", fdwDiagnostic("42704", `option "absent" not found`)))
		run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_options'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{a=table}"}}))
	})
}

func TestReferenceFDWDiagnostics(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE FOREIGN DATA WRAPPER fdw_meta", "42710", `foreign-data wrapper "fdw_meta" already exists`},
			{"CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", "42710", `server "fdw_server" already exists`},
			{"CREATE SERVER absent FOREIGN DATA WRAPPER fdw_absent", "42704", `foreign-data wrapper "fdw_absent" does not exist`},
			{"ALTER SERVER fdw_server OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{"ALTER SERVER fdw_server OPTIONS(\"x=y\" 'v')", "22023", `invalid option name "x=y": must not contain "="`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, fdwDiagnostic(tc.code, tc.message))
				s.Prepared = prepared
				run(s)
			}
		}
		q := "CREATE FOREIGN DATA WRAPPER \"é\" unexpected"
		d := fdwDiagnostic("42601", `syntax error at or near "unexpected"`)
		d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "unexpected")])) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			} else {
				s.ErrorStage = "execute"
			}
			run(s)
		}
		validator := "fdw_sql_validator"
		if b.PG {
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS postgres_fdw", 0))
			validator = "postgres_fdw_validator"
		}
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, tc := range []struct{ q, severity, code, message string }{
			{"CREATE SERVER IF NOT EXISTS fdw_server FOREIGN DATA WRAPPER fdw_meta", "NOTICE", "42710", `server "fdw_server" already exists, skipping`},
			{"DROP SERVER IF EXISTS fdw_absent", "NOTICE", "00000", `server "fdw_absent" does not exist, skipping`},
			{"ALTER FOREIGN DATA WRAPPER fdw_meta NO HANDLER", "WARNING", "01000", "changing the foreign-data wrapper handler can change behavior of existing foreign tables"},
			{"ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR " + validator, "WARNING", "01000", "changing the foreign-data wrapper validator can cause the options for dependent objects to become invalid"},
			{"ALTER FOREIGN DATA WRAPPER fdw_meta NO VALIDATOR", "", "", ""},
		} {
			notices = nil
			fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec(tc.q, 0))
			var want []fixture.Diagnostic
			if tc.severity != "" {
				want = []fixture.Diagnostic{{Severity: tc.severity, SeverityUnlocalized: tc.severity, Code: tc.code, Message: tc.message}}
			}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("%s notices got %#v want %#v", tc.q, notices, want)
			}
		}
	})
}

func TestReferenceFDWTransactions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		run(fixture.Exec("BEGIN", 0))
		fdwMetadata(run)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fdwMetadata(run)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_options", 0))
		run(fixture.Exec("ALTER SERVER fdw_server OPTIONS(a 'transient')", 0))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT before_options", 0))
		run(fixture.Select("SELECT srvoptions IS NULL AS restored FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"restored"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("DROP FOREIGN DATA WRAPPER fdw_meta CASCADE", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_server'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceFDWDependencies(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_dep(id integer) SERVER fdw_server", 0))
		run(fixture.Exec("CREATE VIEW fdw_dep_view AS SELECT id FROM fdw_dep", 0))
		tableDetail := "view fdw_dep_view depends on foreign table fdw_dep"
		serverDetail := "foreign table fdw_dep depends on server fdw_server\n" + tableDetail
		wrapperDetail := "server fdw_server depends on foreign-data wrapper fdw_meta\n" + serverDetail
		run(fixture.ErrorDiagnostic("DROP SERVER fdw_server", fixture.PgDependRestrict("server fdw_server", serverDetail)))
		run(fixture.ErrorDiagnostic("DROP FOREIGN DATA WRAPPER fdw_meta", fixture.PgDependRestrict("foreign-data wrapper fdw_meta", wrapperDetail)))
		run(fixture.ErrorDiagnostic("DROP FOREIGN TABLE fdw_dep", fixture.PgDependRestrict("foreign table fdw_dep", tableDetail)))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP SERVER fdw_server CASCADE", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN ('fdw_dep','fdw_dep_view')", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("DROP SERVER fdw_server CASCADE", 0))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN ('fdw_dep','fdw_dep_view')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceFDWGrantDiagnostics(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwMetadata(run)
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO fdw_reader", 0))
		run(fixture.Exec("CREATE TABLE fdw_grant_before AS SELECT 'wrapper'::text AS kind,oid AS object_oid,fdwacl::text AS acl FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta' UNION ALL SELECT 'server'::text,oid,srvacl::text FROM pg_foreign_server WHERE srvname='fdw_server'", 2))
		unchanged := func(t *testing.T) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT b.kind,w.oid=b.object_oid AS same_object,w.fdwacl::text=b.acl AS same_acl FROM pg_foreign_data_wrapper w CROSS JOIN fdw_grant_before b WHERE b.kind='wrapper' AND w.fdwname='fdw_meta' UNION ALL SELECT b.kind,s.oid=b.object_oid,s.srvacl::text=b.acl FROM pg_foreign_server s CROSS JOIN fdw_grant_before b WHERE b.kind='server' AND s.srvname='fdw_server' ORDER BY kind", []string{"kind", "same_object", "same_acl"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"server", true, true}, {"wrapper", true, true}}))
		}
		unchanged(t)
		before := reader.snapshot()
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepared"
			}
			for _, object := range []struct{ sql, label string }{
				{"FOREIGN DATA WRAPPER fdw_meta", "foreign-data wrapper"},
				{"FOREIGN SERVER fdw_server", "foreign server"},
			} {
				for _, action := range []string{"GRANT", "REVOKE"} {
					preposition := " TO "
					if action == "REVOKE" {
						preposition = " FROM "
					}
					for _, privilege := range []string{"SELECT", "UPDATE", "CREATE", "USAGE, SELECT", "SELECT(id)", "USAGE(id)"} {
						q := action + " " + privilege + " ON " + object.sql + preposition + "fdw_reader"
						if !t.Run(mode+"/"+q, func(t *testing.T) {
							invalid := privilege
							if privilege == "USAGE, SELECT" {
								invalid = "SELECT"
							}
							message := "invalid privilege type " + invalid + " for " + object.label
							if strings.Contains(privilege, "(") {
								message = "column privileges are only valid for relations"
							}
							step := fixture.ErrorDiagnostic(q, fdwDiagnostic("0LP01", message))
							step.Prepared = prepared
							step.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
							unchanged(t)
							if !b.PG && reader.snapshot() != before {
								t.Fatal("invalid privilege invoked application callbacks", before, reader.snapshot())
							}
						}) {
							t.FailNow()
						}
					}
				}
			}
		}
		// Both preexisting USAGE grants still authorize their real operations
		// after every rejected GRANT/REVOKE, not just identical ACL rendering.
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.Exec("CREATE SERVER fdw_grant_probe FOREIGN DATA WRAPPER fdw_meta", 0))
		run(fixture.Exec("DROP SERVER fdw_grant_probe", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_server", 0))
		run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_server", 0))
		run(fixture.Exec("RESET ROLE", 0))
		unchanged(t)
	})
}
