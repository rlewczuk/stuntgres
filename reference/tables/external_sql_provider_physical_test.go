package reference_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderPhysical(t *testing.T) {
	p, provider, handler := externalSQLPhysicalScenario(t)
	handler.action = func(ctx context.Context, _ stuntgres.ForeignRequest, c stuntgres.ExternalSQLConnection) error {
		return externalSQLCheckRemote(ctx, c)
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			externalSQLProviderInstallPhysical(t, b, run)
			externalSQLProviderValues(t, p, b, c, "external_sql_data")
			if !b.PG {
				for _, wire := range provider.snapshot() {
					externalSQLBackendGone(t, p.Ctx, wire.info.BackendPID)
				}
			}
		})
	}
}

func externalSQLProviderInstallPhysical(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	if b.PG {
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS postgres_fdw", 0))
		run(fixture.Exec("CREATE SERVER external_sql_live FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER external_sql_live OPTIONS(user 'stuntgres',password 'stuntgres')", 0))
	} else {
		// Registration supplies this genuine host handler; there is no
		// stock postgres_fdw implementation or substitute transport in engine.
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER external_sql_native HANDLER external_sql_fixture_handler", 0))
		// Only the remote fixture's DSN is interpreted by its test transport
		// library. The native Driver's exact opaque DSN is never conninfo.
		config, err := pgx.ParseConfig(fixture.DSN)
		if err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(fmt.Sprintf("CREATE SERVER external_sql_live FOREIGN DATA WRAPPER external_sql_native OPTIONS(host %s,port '%d',dbname %s)", pq.QuoteLiteral(config.Host), config.Port, pq.QuoteLiteral(config.Database)), 0))
		run(fixture.Exec(fmt.Sprintf("CREATE USER MAPPING FOR CURRENT_USER SERVER external_sql_live OPTIONS(user %s,password %s)", pq.QuoteLiteral(config.User), pq.QuoteLiteral(config.Password)), 0))
	}
	run(fixture.Exec("CREATE FOREIGN TABLE external_sql_data(id integer OPTIONS(column_name 'remote_id') NOT NULL,label text OPTIONS(column_name 'remote_label'),flag boolean OPTIONS(column_name 'remote_flag'),bytes bytea OPTIONS(column_name 'remote_bytes')) SERVER external_sql_live OPTIONS(schema_name 'external_sql_source',table_name 'data')", 0))
}
