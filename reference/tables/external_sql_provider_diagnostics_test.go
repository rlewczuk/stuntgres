package reference_test

import (
	"context"
	"reflect"
	"runtime"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderDiagnosticsAndNotifications(t *testing.T) {
	p, provider, handler := externalSQLPhysicalScenario(t)
	for _, q := range []string{
		"CREATE SCHEMA provider_remote",
		"CREATE TABLE provider_remote.err(id integer CONSTRAINT provider_remote_pk PRIMARY KEY)",
		"INSERT INTO provider_remote.err VALUES(1)",
		"CREATE FUNCTION provider_remote.raise_error() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='external provider failure', DETAIL='owned detail', HINT='owned hint'; END$$",
		"CREATE VIEW provider_remote.error_rows AS SELECT 1/(id-id) AS v FROM provider_remote.err",
	} {
		if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	raw := externalSQLRawConnection(t, p, provider, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: fixture.DSN})
	cases := []struct {
		query      string
		diagnostic fixture.Diagnostic
	}{
		{"SELECT 1/0", fixture.ControlsReferenceDiagnostic("22012", "division by zero")},
		{"SELECT missing_provider_column", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing_provider_column" does not exist`, Position: "8"}},
		{"/*é*/ SELECT missing_provider_column", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing_provider_column" does not exist`, Position: "14"}},
		{"INSERT INTO provider_remote.err VALUES(1)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "provider_remote_pk"`, Detail: "Key (id)=(1) already exists.", Schema: "provider_remote", Table: "err", Constraint: "provider_remote_pk"}},
		{"SELECT provider_remote.raise_error()", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "P0001", Message: "external provider failure", Detail: "owned detail", Hint: "owned hint", Context: "PL/pgSQL function provider_remote.raise_error() line 1 at RAISE"}},
	}
	for _, tc := range cases {
		results, err := raw.Execute(p.Ctx, tc.query)
		fixture.AssertDiagnostic(t, err, &tc.diagnostic)
		if results != nil {
			t.Fatal("failed remote Execute exposed success results", results)
		}
		externalSQLCommand(t, p.Ctx, raw, "SELECT 42")
		fixture.AssertDiagnostic(t, err, &tc.diagnostic) // Diagnostic owns all fields.
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			externalSQLProviderInstallPhysical(t, b, run)
			run(fixture.Exec("CREATE FOREIGN TABLE external_sql_error(v integer) SERVER external_sql_live OPTIONS(schema_name 'provider_remote',table_name 'error_rows')", 0))
			want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			if b.PG {
				want.Context = "remote SQL command: SELECT v FROM provider_remote.error_rows"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT v FROM external_sql_error", want)
				s.SelectRows, s.Prepared = true, prepared
				run(s)
			}
			run(fixture.Exec("CREATE FOREIGN TABLE external_sql_badtype(label integer OPTIONS(column_name 'remote_label')) SERVER external_sql_live OPTIONS(schema_name 'external_sql_source',table_name 'data')", 0))
			conversion := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "x"`)
			conversion.Context = `column "label" of foreign table "external_sql_badtype"`
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT label FROM external_sql_badtype", conversion)
				s.SelectRows, s.Prepared = true, prepared
				run(s)
			}
			if b.PG {
				return
			}
			for _, tc := range cases {
				handler.action = func(ctx context.Context, _ stuntgres.ForeignRequest, connection stuntgres.ExternalSQLConnection) error {
					_, err := connection.Execute(ctx, tc.query)
					return err
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT id FROM external_sql_data", tc.diagnostic)
					s.SelectRows, s.Prepared = true, prepared
					run(s)
				}
			}
			handler.action = nil
		})
	}

	// Remote LISTEN resources belong to the genuine native physical session,
	// separately from the FDW participant that opens them in its callback.
	native, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	if _, err := native.ExecContext(p.Ctx, "LISTEN external_sql_events"); err != nil {
		t.Fatal(err)
	}
	// The preceding backend cleanup removed the SQL wrapper, not registration.
	externalSQLProviderInstallPhysical(t, externalSQLNativeBackend(p), func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) })
	var listeners []stuntgres.ExternalSQLConnection
	handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ExternalSQLConnection) error {
		for range 2 {
			listener, err := r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
			if err != nil {
				return err
			}
			listeners = append(listeners, listener)
			if _, err := listener.Execute(ctx, "LISTEN external_sql_events"); err != nil {
				return err
			}
		}
		return nil
	}
	rows, err := native.QueryContext(p.Ctx, "SELECT id FROM external_sql_data WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("notification owner did not execute real foreign source", rows.Err())
	}
	var id int64
	if err := rows.Scan(&id); err != nil || id != 1 {
		rows.Close()
		t.Fatal(id, err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("notification owner row tail", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	handler.action = nil
	externalSQLNotificationProof(t, p, raw, listeners)
	if _, err := p.Memory.ExecContext(p.Ctx, "NOTIFY external_sql_events,'local-sentinel'"); err != nil {
		t.Fatal(err)
	}
	local, err := stuntgres.WaitForNotification(p.Ctx, native)
	if err != nil || local.Channel != "external_sql_events" || local.Payload != "local-sentinel" || local.PID == 0 {
		t.Fatal("remote event leaked into local notification queue", local, err)
	}
	for _, listener := range listeners {
		pid := listener.Info().BackendPID
		if err := listener.Close(p.Ctx); err != nil {
			t.Fatal(err)
		}
		externalSQLBackendGone(t, p.Ctx, pid)
	}
}

func externalSQLNotificationProof(t *testing.T, p *fixture.Pair, sender stuntgres.ExternalSQLConnection, listeners []stuntgres.ExternalSQLConnection) {
	t.Helper()
	externalSQLCommand(t, p.Ctx, sender, "BEGIN; NOTIFY external_sql_events,'owned'; NOTIFY external_sql_events,'owned'; NOTIFY external_sql_events,'second'; COMMIT")
	externalSQLCommand(t, p.Ctx, sender, "BEGIN; NOTIFY external_sql_events,'rolled-back'; ROLLBACK; NOTIFY external_sql_events; NOTIFY external_sql_events,'sentinel'")
	pid := sender.Info().BackendPID
	want := []stuntgres.ExternalSQLNotification{{Channel: "external_sql_events", BackendPID: pid, Payload: "owned"}, {Channel: "external_sql_events", BackendPID: pid, Payload: "second"}, {Channel: "external_sql_events", BackendPID: pid, Payload: ""}, {Channel: "external_sql_events", BackendPID: pid, Payload: "sentinel"}}
	for _, listener := range listeners {
		var got []stuntgres.ExternalSQLNotification
		for len(got) < len(want) {
			events, err := listener.DrainNotifications(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, events...)
			if err := p.Ctx.Err(); err != nil {
				t.Fatal("remote notifications did not arrive", got, err)
			}
			runtime.Gosched()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("remote notification order/duplicates/rollback/PID", got, want)
		}
		if more, err := listener.DrainNotifications(p.Ctx); err != nil || len(more) != 0 {
			t.Fatal("notification queue did not drain", more, err)
		}
	}
}
