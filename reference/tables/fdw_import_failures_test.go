package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWImportPermissions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		beforeDenied := r.snapshot()
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SET ROLE fdw_reader", 0))
			for _, destination := range []string{"fdw_import", "fdw_missing_destination"} {
				s := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO "+destination, fdwDiagnostic("42501", "permission denied for foreign server fdw_live"))
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader", 0))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SET ROLE fdw_reader", 0))
			s := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", fdwDiagnostic("42501", "permission denied for schema fdw_import"))
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA fdw_import TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", fdwDiagnostic("42704", `user mapping not found for user "fdw_reader", server "fdw_live"`)))
		if !b.PG && r.snapshot() != beforeDenied {
			t.Fatal("permission/mapping preflight invoked provider")
		}
		run(fixture.Exec("RESET ROLE", 0))
		options := "user 'postgres'"
		if b.PG {
			options = "user 'stuntgres',password 'stuntgres',password_required 'false'"
		}
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+options+")", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT c.relname,r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='fdw_import' ORDER BY c.relname", []string{"relname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"import_a", "fdw_reader"}, {"import_b", "fdw_reader"}}))
	})
}

// Cancellation is injected at a later real option validator, after actual
// remote discovery and staging of the earlier table. No fabricated definitions
// or timing-dependent sleeps stand in for catalog I/O/publication.
func TestReferenceFDWImportCancellation(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		run(fixture.Exec("CREATE TABLE fdw_import.sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_import.sentinel VALUES(99)", 1))
		q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import"
		if b.PG {
			run(fixture.Exec(q, 0))
			fdwImportMetadataWithoutSentinel(run, true)
			return // Go callback cancellation identity has no PostgreSQL API equivalent.
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		r.mu.Lock()
		r.validateHook = func(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
			if kind == stuntgres.ForeignTable && fdwOption(options, "table_name") == "import_b" {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return ctx.Err()
		}
		r.mu.Unlock()
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		done := make(chan error, 1)
		before := r.snapshot()
		go func() { _, err := c.ExecContext(ctx, q); done <- err }()
		joined, released := false, false
		defer func() {
			cancel()
			if !released {
				close(release)
			}
			if !joined {
				<-done
			}
			r.mu.Lock()
			r.validateHook = nil
			r.mu.Unlock()
		}()
		select {
		case <-entered:
		case err := <-done:
			joined = true
			t.Fatalf("validator gate not reached: %v", err)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' AND c.relkind='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		cancel()
		close(release)
		released = true
		err := <-done
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
		fdwReleased(t, r, before, 0)
		fdwImportBag(run, "sentinel")
		run(fixture.Select("SELECT v FROM fdw_import.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('import_a','import_b')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		r.mu.Lock()
		r.validateHook = nil
		r.mu.Unlock()
		run(fixture.Exec(q, 0))
		fdwImportMetadataWithoutSentinel(run, true)
	})
}

func fdwImportMetadataWithoutSentinel(run func(fixture.Step), notNull bool) {
	// The metadata helper intentionally inspects every relation: drop only the
	// test's ordinary sentinel after asserting it survived the failed operation.
	run(fixture.Exec("DROP TABLE fdw_import.sentinel", 0))
	fdwImportMetadata(run, notNull)
}

func TestReferenceFDWImportMalformedDefinitions(t *testing.T) {
	for _, fault := range []string{"type", "default", "check", "options", "ordinary-error", "sql-context", "unsupported-source", "remote-authentication", "remote-unreachable"} {
		t.Run(fault, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwImportSource(t, p, b, c, r, run)
				q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import"
				if fault == "remote-authentication" || fault == "remote-unreachable" {
					if fault == "remote-unreachable" {
						if !b.PG {
							return
						} // Native SQL source has no network transport.
						run(fixture.Exec("ALTER SERVER fdw_live OPTIONS(SET port '1')", 0))
					} else {
						action := "ADD"
						if b.PG {
							action = "SET"
						}
						run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS("+action+" user 'fdw_absent_source_login')", 0))
					}
					before := r.snapshot()
					_, err := c.ExecContext(p.Ctx, q)
					if err == nil {
						t.Fatal("failed source connection silently imported an empty schema")
					}
					if b.PG {
						want := fdwDiagnostic("08001", `could not connect to server "fdw_live"`)
						want.Detail = "connection to server at \"127.0.0.1\", port 5432 failed: FATAL:  role \"fdw_absent_source_login\" does not exist"
						if fault == "remote-unreachable" {
							want.Detail = "connection to server at \"127.0.0.1\", port 1 failed: Connection refused\n\tIs the server running on that host and accepting TCP/IP connections?"
						}
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						fixture.TransactionOrdinary(t, err, `stuntgres: authenticated role "fdw_absent_source_login" does not exist`)
					}
					if !b.PG {
						fdwReleased(t, r, before, 0)
					}
					fdwImportBag(run)
					return
				}
				if b.PG {
					// Genuine loopback enum exists in both remote and local catalogs.
					// PostgreSQL imports its qualified identity without a missing-type error.
					if fault != "type" {
						return
					}
					fdwSourceExec(t, p, b, r, run, "DROP TABLE fdw_source.import_b", 0)
					fdwSourceExec(t, p, b, r, run, "CREATE TYPE fdw_source.import_enum AS ENUM('source')", 0)
					fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.import_b(key fdw_source.import_enum)", 0)
					fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.import_b VALUES('source')", 1)
					run(fixture.Exec(q, 0))
					fdwImportBag(run, "import_a", "import_b")
					run(fixture.Select("SELECT a.atttypid='fdw_source.import_enum'::regtype::oid AS exact_type FROM pg_attribute a WHERE a.attrelid='fdw_import.import_b'::regclass::oid AND a.attname='key'", []string{"exact_type"}, []string{"BOOL"}, [][]any{{true}}))
					run(fixture.Select("SELECT key::text AS key FROM fdw_import.import_b", []string{"key"}, []string{"TEXT"}, [][]any{{"source"}}))
					return
				}
				if fault == "unsupported-source" {
					fdwSourceExec(t, p, b, r, run, "DROP TABLE fdw_source.import_b", 0)
					fdwSourceExec(t, p, b, r, run, "CREATE TYPE fdw_source.import_enum AS ENUM('source')", 0)
					fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.import_b(key fdw_source.import_enum)", 0)
					before := r.snapshot()
					_, err := c.ExecContext(p.Ctx, q)
					fixture.TransactionOrdinary(t, err, `SQL reader cannot import source type ""`)
					fdwReleased(t, r, before, 0)
					fdwImportBag(run)
					return
				}
				ordinary := errors.New("actual later definition I/O failed")
				r.mu.Lock()
				r.importDefinitionHook = func(ctx context.Context, d *stuntgres.ForeignTableDefinition) error {
					if d.Name != "import_b" {
						return ctx.Err()
					}
					switch fault {
					case "type":
						d.Columns[0].Type = "fdw_import.missing_type"
					case "default":
						d.Columns[0].Default = "("
					case "check":
						d.Checks = []string{"("}
					case "options":
						d.Options = append(d.Options, stuntgres.ForeignOption{Name: "table_name", Value: "duplicate"})
					case "ordinary-error":
						return ordinary
					case "sql-context":
						return &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "actual remote definition rejected", Context: "provider catalog"}
					}
					return ctx.Err()
				}
				r.mu.Unlock()
				defer func() { r.mu.Lock(); r.importDefinitionHook = nil; r.mu.Unlock() }()
				before := r.snapshot()
				_, err := c.ExecContext(p.Ctx, q)
				if fault == "ordinary-error" {
					if !errors.Is(err, ordinary) {
						t.Fatalf("ordinary identity=%v", err)
					}
				} else if fault == "sql-context" {
					want := fdwDiagnostic("22023", "actual remote definition rejected")
					want.Context = "provider catalog"
					fixture.AssertDiagnostic(t, err, &want)
				} else {
					if err == nil {
						t.Fatal("malformed actual later definition succeeded")
					}
					// These positions belong to parsed structured definition fragments,
					// not offsets into the original IMPORT statement.
					want := fdwDiagnostic("42710", `option "table_name" provided more than once`)
					switch fault {
					case "type":
						want = fdwDiagnostic("42704", `type "fdw_import.missing_type" does not exist`)
						want.Position = "1"
					case "default", "check":
						want = fdwDiagnostic("42601", `syntax error at or near ")"`)
						want.Position = "10"
					}
					want.Context = `importing foreign table "import_b"`
					fixture.AssertDiagnostic(t, err, &want)
				}
				fdwReleased(t, r, before, 0)
				fdwImportBag(run)
				run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_class c ON d.objid=c.oid WHERE c.relname IN('import_a','import_b')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				r.mu.Lock()
				r.importDefinitionHook = nil
				r.mu.Unlock()
				run(fixture.Exec(q, 0))
				fdwImportBag(run, "import_a", "import_b")
			})
		})
	}
}
