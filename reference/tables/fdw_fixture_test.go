package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwSQLReader, func(fixture.Step))) {
	t.Helper()
	fdwConfiguredScenario(t, false, body)
}

func fdwWriteScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwSQLReader, func(fixture.Step))) {
	t.Helper()
	fdwConfiguredScenario(t, true, body)
}

func fdwConfiguredScenario(t *testing.T, writable bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwSQLReader, func(fixture.Step))) {
	t.Helper()
	fdwConfiguredGraphScenario(t, writable, false, body)
}

func fdwConfiguredGraphScenario(t *testing.T, writable, graph bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwSQLReader, func(fixture.Step))) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"fdw_reader", "fdw_other", "fdw_owner"})
	fixture.SchemaReferenceCleanup(t, "fdw_source")
	fixture.SchemaReferenceCleanup(t, "fdw_import")
	fixture.SchemaReferenceCleanup(t, "fdw_empty")
	reader := newFDWSQLReader(t)
	if writable {
		reader.writer = &fdwSQLWriter{fdwSQLReader: reader, batchSize: 2}
	}
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		if err := reader.configure(d); err != nil {
			return err
		}
		if graph {
			return d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_graph_validator"}, Validate: reader.validateGraph})
		}
		return nil
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Errorf("FDW connection close: %v", err)
				}
			})
			t.Cleanup(func() {
				fdwCleanup(t, c)
			})
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			body(t, p, b, c, reader, run)
		})
	}
}

// Teardown owns a bounded lifetime independent of failed or canceled case work.
// Roll back and reset the same physical connection before removing its objects.
func fdwCleanup(t *testing.T, c *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, q := range []string{"ROLLBACK", "RESET ROLE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdw_bad CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdw_meta CASCADE", "DROP FUNCTION IF EXISTS public.bad_handler()", "DROP SERVER IF EXISTS fdw_live CASCADE", "DROP SERVER IF EXISTS fdw_second CASCADE", "DROP SERVER IF EXISTS fdw_truncate_second CASCADE", "DROP SERVER IF EXISTS fdw_readonly CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdw_readonly_wrapper CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdw_native CASCADE"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Errorf("cleanup %s: %v", q, err)
		}
	}
}

// Preserve the actual configured connector while recording its public SQL
// results through the same middleware as the shared fixture. The caller owns
// the returned pool.
func fdwRecordedPool(t *testing.T, connector driver.Connector) *sql.DB {
	t.Helper()
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(configured)
}

func fdwMetadata(run func(fixture.Step)) {
	run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta NO HANDLER NO VALIDATOR", 0))
	run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", 0))
}

// Both providers read committed source SQL; only provider installation and
// source connection ownership differ between the two backend setups.
func fdwLive(t *testing.T, p *fixture.Pair, b fixture.Backend, reader *fdwSQLReader, run func(fixture.Step)) {
	fdwSourceExec(t, p, b, reader, run, "CREATE SCHEMA fdw_source", 0)
	fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.data(id integer,label text,flag boolean)", 0)
	fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.data VALUES(1,'one',true),(2,NULL,false),(3,'three',NULL)", 3)
	fdwInstallLive(b, run)
}

func fdwInstallLive(b fixture.Backend, run func(fixture.Step)) {
	if b.PG {
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS postgres_fdw", 0))
		run(fixture.Exec("CREATE SERVER fdw_live FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(user 'stuntgres',password 'stuntgres')", 0))
	} else {
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler VALIDATOR fdw_sql_validator", 0))
		run(fixture.Exec("CREATE SERVER fdw_live FOREIGN DATA WRAPPER fdw_native", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
	}
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_data(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
}

func fdwRows(q string, rows [][]any) fixture.Step {
	return fixture.Select(q, []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, rows)
}

func fdwDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}
