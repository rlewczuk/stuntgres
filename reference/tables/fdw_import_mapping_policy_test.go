package reference_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWImportMappingPolicy(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwImportSource(t, p, b, c, r, run)
				run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
				role := "postgres"
				if b.PG {
					role = "stuntgres"
				}
				q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import"
				before := r.snapshot()
				s := fixture.ErrorDiagnostic(q, fdwDiagnostic("42704", `user mapping not found for user "`+role+`", server "fdw_live"`))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				fdwImportBag(run)
				if !b.PG {
					after := r.snapshot()
					if after.openPools != 0 || after.openRows != 0 || after.openCursors != 0 || after.calls != before.calls || after.begins != before.begins || after.queries != before.queries || after.iterations != before.iterations {
						t.Fatalf("reader rejected missing mapping after source hook/acquisition: before=%+v after=%+v", before, after)
					}
				}
				options := "user 'postgres'"
				if b.PG {
					options = "user 'stuntgres',password 'stuntgres'"
				}
				run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_live OPTIONS("+options+")", 0))
				s = fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
				fdwImportMetadata(run, true)
				fdwImportValues(t, p, b, c, run)
				run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a,fdw_import.import_b", 0))
				run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS("+options+")", 0))
				s = fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
				fdwImportBag(run, "import_a", "import_b")
				fdwImportValues(t, p, b, c, run)
				fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT key,note FROM fdw_source.import_b", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(9), "kept"}}))
				if !b.PG {
					// Go registration has no PostgreSQL equivalent. It is an explicit
					// native capability phase, not a mapping-free postgres_fdw claim.
					t.Run("native_mapping_independent", func(t *testing.T) { fdwMappingIndependent(t, p.Ctx, r, prepared) })
				}
			})
		})
	}
}

// This provider's authority is the test-owned source connection, not a mapping.
// Only credentials are adapted: all catalog discovery, type/typmod conversion,
// option construction and later scans remain the existing genuine SQL reader.
// Ordinary fdwSQLReader policy is deliberately not relaxed.
type fdwTrustedImporter struct {
	*fdwSQLReader
	requests []stuntgres.ForeignImportRequest
}

func (r *fdwTrustedImporter) ImportForeignSchema(ctx context.Context, request stuntgres.ForeignImportRequest) ([]stuntgres.ForeignTableDefinition, error) {
	r.requests = append(r.requests, request)
	// The test owns this independent source and explicitly authorizes the
	// delegated discovery as its source owner, regardless of mapping metadata.
	request.MappingPresent = true
	request.MappingOptions = []stuntgres.ForeignOption{{Name: "user", Value: "postgres"}}
	return r.fdwSQLReader.ImportForeignSchema(ctx, request)
}

func fdwMappingIndependent(t *testing.T, ctx context.Context, reader *fdwSQLReader, prepared bool) {
	t.Helper()
	connector, err := reader.driver.OpenConnector(reader.dsn)
	if err != nil {
		t.Fatal(err)
	}
	source := fdwRecordedPool(t, connector)
	defer source.Close()
	sourceRecorder := &fixture.Recorder{RawBackend: fixture.RawNative}
	for _, policy := range []string{"absent", "present_empty", "public", "exact_over_public"} {
		t.Run(policy, func(t *testing.T) {
			provider := &fdwTrustedImporter{fdwSQLReader: reader}
			d := stuntgres.NewDriver()
			if err := reader.configure(d); err != nil {
				t.Fatal(err)
			}
			if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_trusted_handler"}, Wrapper: provider}); err != nil {
				t.Fatal(err)
			}
			connector, err := d.OpenConnector("fdw-trusted/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := fdwRecordedPool(t, connector)
			defer db.Close()
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, recorder, s) }
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_trusted HANDLER fdw_trusted_handler VALIDATOR fdw_sql_validator", 0))
			run(fixture.Exec("CREATE SERVER fdw_trusted FOREIGN DATA WRAPPER fdw_trusted", 0))
			run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
			var wantOptions []stuntgres.ForeignOption
			switch policy {
			case "present_empty":
				run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_trusted", 0))
			case "public", "exact_over_public":
				run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_trusted OPTIONS(user 'stuntgres')", 0))
				wantOptions = []stuntgres.ForeignOption{{Name: "user", Value: "stuntgres"}}
				if policy == "exact_over_public" {
					run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_trusted OPTIONS(user 'postgres')", 0))
					wantOptions = []stuntgres.ForeignOption{{Name: "user", Value: "postgres"}}
				}
			}
			q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_trusted INTO fdw_import"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if len(provider.requests) != 0 {
				t.Fatal("Prepare performed discovery")
			}
			var executor fixture.Executor = c
			if prepared {
				executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			fixture.RunStep(t, ctx, executor, recorder, fixture.Exec(q, 0))
			provider.assertRequests(t, policy != "absent", wantOptions, 1)
			fdwImportBag(run, "import_a", "import_b")
			fdwImportMetadata(run, true)
			// Import authority does not grant mapping-free SELECT. Install the
			// normal mapping before proving real source rows through the engine.
			if policy == "absent" {
				run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_trusted", 0))
			}
			for _, held := range []bool{false, true} {
				s := fixture.Select("SELECT id,label,amount,flag FROM fdw_import.import_a ORDER BY id", []string{"id", "label", "amount", "flag"}, []string{"INT4", "VARCHAR", "NUMERIC", "BOOL"}, [][]any{{int64(1), "alpha", "12.34", true}, {int64(2), nil, nil, false}})
				s.Prepared = held
				run(s)
			}
			run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a,fdw_import.import_b", 0))
			// Restore the original no-mapping policy for the failure witnesses.
			if policy == "absent" {
				run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_trusted", 0))
			}
			missing := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_missing_source FROM SERVER fdw_trusted INTO fdw_import", fdwDiagnostic("HV00Q", `schema "fdw_missing_source" is not present on foreign server "fdw_trusted"`))
			missing.Prepared, missing.ErrorStage = prepared, "execute"
			run(missing)
			fdwImportBag(run)
			run(fixture.Exec("CREATE TABLE fdw_import.import_b(key bigint)", 0))
			run(fixture.Exec("INSERT INTO fdw_import.import_b VALUES(99)", 1))
			collision := fdwDiagnostic("42P07", `relation "import_b" already exists`)
			collision.Context = `importing foreign table "import_b"`
			fixture.RunStep(t, ctx, executor, recorder, fixture.ErrorDiagnostic(q, collision))
			provider.assertRequests(t, policy != "absent", wantOptions, 3)
			fdwImportBag(run, "import_b")
			run(fixture.Select("SELECT key FROM fdw_import.import_b", []string{"key"}, []string{"INT8"}, [][]any{{int64(99)}}))
			fixture.RunStep(t, ctx, source, sourceRecorder, fixture.Select("SELECT key,note FROM fdw_source.import_b", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(9), "kept"}}))
			state := reader.snapshot()
			if state.openPools != 0 || state.openCursors != 0 || state.openRows != 0 {
				t.Fatalf("source resources retained: %+v", state)
			}
		})
	}
}

func (r *fdwTrustedImporter) assertRequests(t *testing.T, present bool, options []stuntgres.ForeignOption, count int) {
	t.Helper()
	if len(r.requests) != count {
		t.Fatalf("discovery requests=%d want=%d", len(r.requests), count)
	}
	for i, request := range r.requests {
		remote := "fdw_source"
		tables := []string{"import_a", "import_b"}
		if i == 1 {
			remote, tables = "fdw_missing_source", nil
		}
		if request.MappingPresent != present || !slices.Equal(request.MappingOptions, options) ||
			request.Role != "postgres" || request.Server != "fdw_trusted" ||
			request.RemoteSchema != remote || request.LocalSchema != "fdw_import" ||
			!slices.Equal(request.Tables, tables) || request.Except ||
			len(request.WrapperOptions) != 0 || len(request.ServerOptions) != 0 || len(request.Options) != 0 {
			t.Fatalf("original discovery request %d=%+v want mapping present=%v options=%+v remote=%s tables=%v", i, request, present, options, remote, tables)
		}
	}
}
