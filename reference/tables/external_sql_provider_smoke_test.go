package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderFeatureSmoke(t *testing.T) {
	p, sourceProvider, handler := externalSQLPhysicalScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("UPDATE external_sql_source.data SET remote_label='x' WHERE remote_id=1", 1))
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			externalSQLProviderInstallPhysical(t, b, run)
			var probe, listener stuntgres.ExternalSQLConnection
			defer func() {
				handler.action = nil
				if probe != nil {
					probe.Close(context.Background())
				}
				if listener != nil {
					listener.Close(context.Background())
				}
			}()
			if !b.PG {
				handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, connection stuntgres.ExternalSQLConnection) error {
					if probe != nil {
						return nil
					}
					if err := externalSQLCheckRemote(ctx, connection); err != nil {
						return err
					}
					var err error
					probe, err = r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
					if err != nil {
						return err
					}
					listener, err = r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
					if err != nil {
						return err
					}
					_, err = listener.Execute(ctx, "LISTEN external_sql_events")
					return err
				}
			}
			externalSQLProviderValues(t, p, b, c, "external_sql_data")
			if b.PG {
				return
			}
			handler.action = nil
			_, err := probe.Execute(p.Ctx, "SELECT missing_provider_column")
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing_provider_column" does not exist`, Position: "8"}
			fixture.AssertDiagnostic(t, err, &want)
			externalSQLLock(t, p, 718032)
			var canceled []stuntgres.ExternalSQLResult
			var failure error
			done := make(chan struct{})
			go func() {
				defer close(done)
				canceled, failure = probe.Execute(p.Ctx, "SELECT pg_advisory_xact_lock(718032)")
			}()
			defer func() {
				cleanup, end := context.WithTimeout(context.Background(), fixture.Liveness)
				defer end()
				_ = probe.Cancel(cleanup)
				select {
				case <-done:
				case <-cleanup.Done():
					t.Error("smoke cancellation worker did not join")
				}
			}()
			externalSQLWaitBlocked(t, p.Ctx, probe.Info().BackendPID)
			if err := probe.Cancel(p.Ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			cancellation := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, failure, &cancellation)
			if canceled != nil {
				t.Fatal("smoke cancellation published rows", canceled)
			}
			externalSQLNotificationProof(t, p, probe, []stuntgres.ExternalSQLConnection{listener})

			for _, s := range []fixture.Step{
				fixture.Exec("CREATE EXTENSION dblink", 0),
				fixture.Exec("CREATE TABLE external_sql_key(id integer PRIMARY KEY)", 0),
				fixture.Exec("INSERT INTO external_sql_key VALUES(7)", 1),
				fixture.Exec("CREATE TABLE external_sql_smoke_local(v integer)", 0),
				fixture.Exec("INSERT INTO external_sql_smoke_local VALUES(73)", 1),
				fixture.Exec("CREATE VIEW external_sql_smoke_saved AS SELECT id,label,flag,bytes FROM external_sql_data", 0),
			} {
				run(s)
			}
			var dump bytes.Buffer
			before := len(sourceProvider.snapshot())
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if len(sourceProvider.snapshot()) != before {
				t.Fatal("smoke dump invoked transport")
			}
			old, err := c.QueryContext(p.Ctx, "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			externalSQLOldRows(t, old)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("UPDATE external_sql_source.data SET remote_label='smoke-later' WHERE remote_id=1", 1))
			destinationProvider := &externalSQLProvider{}
			d := stuntgres.NewDriver()
			if err := externalSQLConfigure(d, destinationProvider, &externalSQLHandler{}, externalSQLFixtureVersion); err != nil {
				t.Fatal(err)
			}
			connector, err := d.OpenConnector("external-sql-smoke-destination")
			if err != nil {
				t.Fatal(err)
			}
			native := sql.OpenDB(connector)
			db, recorder := fixture.RecordedNativePool(t, connector)
			defer db.Close()
			defer native.Close()
			if err := stuntgres.Restore(p.Ctx, native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			if len(destinationProvider.snapshot()) != 0 {
				t.Fatal("smoke restore opened physical connection")
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT id,label,flag,bytes FROM external_sql_smoke_saved WHERE id=1", []string{"id", "label", "flag", "bytes"}, []string{"INT4", "TEXT", "BOOL", "BYTEA"}, [][]any{{int64(1), "smoke-later", true, "\x00\xff"}}),
				fixture.Select("SELECT v FROM external_sql_smoke_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}),
				fixture.Select("SELECT position,colname FROM dblink_get_pkey('external_sql_key') ORDER BY position", []string{"position", "colname"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "id"}}),
				fixture.Select("SELECT dblink_build_sql_delete('external_sql_key','1'::int2vector,1,'{7}'::text[]) AS sql", []string{"sql"}, []string{"TEXT"}, [][]any{{"DELETE FROM external_sql_key WHERE id = '7'"}}),
			} {
				fixture.RunStep(t, p.Ctx, db, recorder, s)
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
			}
			for _, wire := range destinationProvider.snapshot() {
				if wire.info.BackendPID == probe.Info().BackendPID || wire.info.BackendPID == listener.Info().BackendPID {
					t.Fatal("destination reused source physical handle")
				}
				externalSQLBackendGone(t, p.Ctx, wire.info.BackendPID)
			}
			for _, owned := range []stuntgres.ExternalSQLConnection{probe, listener} {
				pid := owned.Info().BackendPID
				if err := owned.Close(p.Ctx); err != nil {
					t.Fatal(err)
				}
				externalSQLBackendGone(t, p.Ctx, pid)
			}
		})
	}
}
