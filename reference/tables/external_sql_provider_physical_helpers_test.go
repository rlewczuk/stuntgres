package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func externalSQLPhysicalScenario(t *testing.T) (*fixture.Pair, *externalSQLProvider, *externalSQLHandler) {
	t.Helper()
	if !*fixture.Mode {
		t.Skip("physical external SQL provider comparison requires REFERENCE=1")
	}
	fixture.SchemaReferenceCleanup(t, "external_sql_source", "provider_remote")
	provider, handler := &externalSQLProvider{}, &externalSQLHandler{}
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		return externalSQLConfigure(d, provider, handler, externalSQLFixtureVersion)
	})
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE SCHEMA external_sql_source", 0),
		fixture.Exec("CREATE TABLE external_sql_source.data(remote_id integer NOT NULL,remote_label text,remote_flag boolean,remote_bytes bytea)", 0),
		fixture.Exec("INSERT INTO external_sql_source.data VALUES(1,'x',true,decode('00ff','hex')),(2,NULL,false,NULL),(2,NULL,false,NULL),(3,'',NULL,decode('','hex'))", 4),
	} {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
	}
	return p, provider, handler
}

// Return errors to the SQL caller: Fatal/Goexit inside an active host callback
// skips the engine's request unwind and can strand database/sql connection Close.
func externalSQLCheckRemote(ctx context.Context, connection stuntgres.ExternalSQLConnection) error {
	info := connection.Info()
	var observer int32
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&observer); err != nil {
		return fmt.Errorf("remote observer identity: %w", err)
	}
	if info.BackendPID <= 0 || info.BackendPID == observer || info.User != "stuntgres" || info.Database != "stuntgres_reference" || info.ProviderVersion != externalSQLFixtureVersion || info.CredentialMechanism != "unknown" {
		return fmt.Errorf("remote connection identity: got %#v, observer PID %d", info, observer)
	}
	var count int64
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1 AND usename='stuntgres' AND datname='stuntgres_reference' AND application_name LIKE 'external_sql_fixture_%'", info.BackendPID).Scan(&count); err != nil {
		return fmt.Errorf("independent remote backend witness: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("independent remote backend witness: got %d, want 1 for PID %d", count, info.BackendPID)
	}
	const q = "SELECT remote_id,remote_label,remote_flag,remote_bytes FROM external_sql_source.data ORDER BY remote_id"
	const emptyDifferent = "SELECT remote_label::varchar(7) AS empty_label FROM external_sql_source.data WHERE false"
	results, err := connection.Execute(ctx, q+"; "+q+" LIMIT 0; "+emptyDifferent)
	if err != nil {
		return fmt.Errorf("remote materialization: %w", err)
	}
	if len(results) != 3 {
		return fmt.Errorf("remote materialization: got %d results, want 3: %#v", len(results), results)
	}
	columns := []stuntgres.ExternalSQLColumn{{Name: "remote_id", TypeOID: 23, TypeModifier: -1}, {Name: "remote_label", TypeOID: 25, TypeModifier: -1}, {Name: "remote_flag", TypeOID: 16, TypeModifier: -1}, {Name: "remote_bytes", TypeOID: 17, TypeModifier: -1}}
	emptyColumns := []stuntgres.ExternalSQLColumn{{Name: "empty_label", TypeOID: 1043, TypeModifier: 11}}
	cells := []stuntgres.ExternalSQLCell{{Text: "1"}, {Text: "x"}, {Text: "t"}, {Text: `\x00ff`}, {Text: "2"}, {Null: true}, {Text: "f"}, {Null: true}, {Text: "2"}, {Null: true}, {Text: "f"}, {Null: true}, {Text: "3"}, {Text: ""}, {Null: true}, {Text: `\x`}}
	for i, result := range results {
		wantColumns := columns
		if i == 2 {
			wantColumns = emptyColumns
		}
		if !result.Tuple || !reflect.DeepEqual(result.Columns, wantColumns) {
			return fmt.Errorf("raw descriptor result %d: tuple=%t, got %#v, want %#v", i, result.Tuple, result.Columns, wantColumns)
		}
		if i == 0 && (result.RowCount != 4 || result.RowsAffected != 4 || result.CommandTag != "SELECT 4" || !reflect.DeepEqual(result.Cells, cells)) {
			return fmt.Errorf("raw remote values result %d: got %#v, want SELECT 4 with cells %#v", i, result, cells)
		}
		if i > 0 && (result.RowCount != 0 || result.RowsAffected != 0 || result.CommandTag != "SELECT 0" || len(result.Cells) != 0) {
			return fmt.Errorf("empty raw result %d: got %#v, want SELECT 0 with no cells", i, result)
		}
	}
	if _, err := connection.Execute(ctx, "SELECT 42"); err != nil {
		return fmt.Errorf("later remote activity: %w", err)
	}
	if !reflect.DeepEqual(results[0].Columns, columns) || !reflect.DeepEqual(results[0].Cells, cells) || !reflect.DeepEqual(results[1].Columns, columns) || !reflect.DeepEqual(results[2].Columns, emptyColumns) {
		return fmt.Errorf("later activity mutated owned results: %#v", results)
	}
	return nil
}

func externalSQLBackendGone(t *testing.T, ctx context.Context, pid int32) {
	t.Helper()
	for {
		var count int64
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("remote backend did not exit", pid, err)
		}
		runtime.Gosched()
	}
}

func externalSQLRawConnection(t *testing.T, p *fixture.Pair, provider *externalSQLProvider, request stuntgres.ExternalSQLConnectRequest) stuntgres.ExternalSQLConnection {
	t.Helper()
	c, err := provider.Open(p.Ctx, request)
	if err != nil {
		if c != nil {
			_ = c.Close(context.WithoutCancel(p.Ctx))
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return c
}

func externalSQLCommand(t *testing.T, ctx context.Context, c stuntgres.ExternalSQLConnection, q string) []stuntgres.ExternalSQLResult {
	t.Helper()
	r, err := c.Execute(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	return r
}

func externalSQLNativeBackend(p *fixture.Pair) fixture.Backend {
	return fixture.Backend{Name: "memory", DB: p.Memory, Recorder: p.MemoryRecord}
}

func externalSQLInstallNative(t *testing.T, p *fixture.Pair) (*sql.Conn, fixture.Backend) {
	t.Helper()
	b := externalSQLNativeBackend(p)
	c := externalSQLProviderConnection(t, p, b)
	externalSQLProviderInstallPhysical(t, b, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) })
	return c, b
}
