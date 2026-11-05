package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWPhysical(t *testing.T) {
	p, provider := postgresFDWPhysicalScenario(t)
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		before := provider.OpenCalls()
		postgresFDWPhysicalInstall(t, b, run)
		if !b.PG && provider.OpenCalls() != before {
			t.Fatal("declarations opened a physical connection")
		}
		run(fixture.Select("SELECT attname,atttypid::integer FROM pg_attribute WHERE attrelid='pf_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "atttypid"}, []string{"NAME", "INT4"}, [][]any{{"id", int64(23)}, {"label", int64(25)}, {"flag", int64(16)}, {"bytes", int64(17)}}))
		externalSQLProviderValues(t, p, b, c, "pf_table")
		pid := postgresFDWPID(t, p, c)
		if !b.PG {
			wires := provider.Snapshot()
			if len(wires) == 0 || wires[len(wires)-1].Info().BackendPID != pid {
				t.Fatal("native SQL did not consume actual bound transport")
			}
			request := provider.LastRequestIdentity()
			if !request.MappingPresent || request.ServerOID == 0 || request.MappingOID == 0 || request.RoleOID == 0 {
				t.Fatalf("lost captured mapping authority: %#v", request)
			}
		}
		// A zero-attribute demand must still consume all four real source rows.
		run(fixture.Select("SELECT count(*) FROM pf_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Select("SELECT 7 AS marker FROM pf_table", []string{"marker"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}}))
		for _, tc := range []struct{ option, value, code, message, remote string }{
			{"column_name", "pf_missing_column", "42703", `column "pf_missing_column" does not exist`, `SELECT pf_missing_column FROM "pf source ""quoted"."data table"`},
			{"column_name", "", "42601", "zero-length delimited identifier at or near \"\"\"\"", `SELECT "" FROM "pf source ""quoted"."data table"`},
		} {
			run(fixture.Exec("ALTER FOREIGN TABLE pf_table ALTER COLUMN id OPTIONS(SET column_name "+fileFDWLiteral(tc.value)+")", 0))
			d := fdwDiagnostic(tc.code, tc.message)
			d.Context = "remote SQL command: " + tc.remote
			postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, d)
			run(fixture.Exec("ALTER FOREIGN TABLE pf_table ALTER COLUMN id OPTIONS(SET column_name 'Remote ID')", 0))
			run(fixture.Select("SELECT id FROM pf_table ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}}))
		}
		run(fixture.Exec("ALTER FOREIGN TABLE pf_table OPTIONS(SET table_name 'pf_missing_table')", 0))
		d := fdwDiagnostic("42P01", `relation "pf source "quoted.pf_missing_table" does not exist`)
		d.Context = `remote SQL command: SELECT "Remote ID" FROM "pf source ""quoted".pf_missing_table`
		postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, d)
		run(fixture.Exec("ALTER FOREIGN TABLE pf_table OPTIONS(SET table_name 'data table')", 0))
		run(fixture.Exec(`ALTER FOREIGN TABLE pf_table OPTIONS(SET schema_name '')`, 0))
		d = fdwDiagnostic("42601", "zero-length delimited identifier at or near \"\"\"\"")
		d.Context = `remote SQL command: SELECT "Remote ID" FROM ""."data table"`
		postgresFDWQueryFailure(t, p, b, c, "SELECT id FROM pf_table", prepared, d)
		run(fixture.Exec(`ALTER FOREIGN TABLE pf_table OPTIONS(SET schema_name 'pf source "quoted')`, 0))
	})
}

func TestReferencePostgresFDWEnvironment(t *testing.T) {
	p, _ := postgresFDWPhysicalScenario(t)
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWPhysicalInstall(t, b, run)
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		// Stock initialization forces ISO format, preserving startup date order.
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD options '-c search_path=public -c TimeZone=Europe/Paris -c DateStyle=SQL,DMY -c IntervalStyle=iso_8601 -c extra_float_digits=0')", 0))
		run(fixture.Select("SELECT path,zone,dates,intervals,digits FROM pf_identity", []string{"path", "zone", "dates", "intervals", "digits"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"pg_catalog", "GMT", "ISO, DMY", "postgres", "3"}}))
		postgresFDWPID(t, p, c)
		// The remote SQL-language function's unqualified helper cannot inherit
		// the local search_path, even though both helpers exist on the endpoint.
		defer postgresFDWRemoteCleanup(t, p, c,
			`DROP VIEW IF EXISTS "pf source ""quoted".environment_call`,
			`DROP FUNCTION IF EXISTS "pf source ""quoted".pf_env_call()`,
			`DROP FUNCTION IF EXISTS "pf source ""quoted".pf_env_helper()`)
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE FUNCTION "pf source ""quoted".pf_env_helper() RETURNS text LANGUAGE SQL AS 'SELECT ''qualified'''`, 0),
			fixture.Exec(`SET check_function_bodies=off`, 0),
			fixture.Exec(`CREATE FUNCTION "pf source ""quoted".pf_env_call() RETURNS text LANGUAGE SQL AS 'SELECT pf_env_helper()'`, 0),
			fixture.Exec(`RESET check_function_bodies`, 0),
			fixture.Exec(`CREATE VIEW "pf source ""quoted".environment_call AS SELECT "pf source ""quoted".pf_env_call() AS value`, 0),
		} {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
		run(fixture.Exec(`CREATE FOREIGN TABLE pf_environment(value text) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'environment_call')`, 0))
		d := fdwDiagnostic("42883", "function pf_env_helper() does not exist")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		d.Context = "SQL function \"pf_env_call\" during inlining\nremote SQL command: SELECT value FROM \"pf source \"\"quoted\".environment_call"
		postgresFDWQueryFailure(t, p, b, c, "SELECT value FROM pf_environment", prepared, d)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`CREATE OR REPLACE FUNCTION "pf source ""quoted".pf_env_call() RETURNS text LANGUAGE SQL AS 'SELECT "pf source ""quoted".pf_env_helper()'`, 0))
		run(fixture.Select("SELECT value FROM pf_environment", []string{"value"}, []string{"TEXT"}, [][]any{{"qualified"}}))
	})
}
