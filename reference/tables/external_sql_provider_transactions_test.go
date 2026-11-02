package reference_test

import (
	"context"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderTransactions(t *testing.T) {
	p, provider, handler := externalSQLPhysicalScenario(t)
	observer := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
	observer(fixture.Exec("CREATE TABLE external_sql_source.marks(id integer)", 0))
	observer(fixture.Exec("CREATE VIEW external_sql_source.settings AS SELECT current_setting('transaction_isolation') AS isolation,current_setting('transaction_read_only') AS readonly", 0))
	raw := externalSQLRawConnection(t, p, provider, stuntgres.ExternalSQLConnectRequest{Conninfo: fixture.DSN})
	count := func(id, n int64) {
		observer(fixture.Select("SELECT count(*) AS n FROM external_sql_source.marks WHERE id=$1", []string{"n"}, []string{"INT8"}, [][]any{{n}}, id))
	}
	externalSQLCommand(t, p.Ctx, raw, "BEGIN; INSERT INTO external_sql_source.marks VALUES(1)")
	count(1, 0)
	externalSQLCommand(t, p.Ctx, raw, "ROLLBACK")
	count(1, 0)
	externalSQLCommand(t, p.Ctx, raw, "BEGIN; INSERT INTO external_sql_source.marks VALUES(1); COMMIT")
	count(1, 1)

	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			externalSQLProviderInstallPhysical(t, b, run)
			run(fixture.Exec("CREATE FOREIGN TABLE external_sql_settings(isolation text,readonly text) SERVER external_sql_live OPTIONS(schema_name 'external_sql_source',table_name 'settings')", 0))
			for _, mode := range []struct{ begin, isolation string }{{"BEGIN", "repeatable read"}, {"BEGIN ISOLATION LEVEL SERIALIZABLE", "serializable"}} {
				run(fixture.Exec(mode.begin, 0))
				run(fixture.Exec("SAVEPOINT external_sql_local_mark", 0))
				settings := fixture.Select("SELECT isolation,readonly FROM external_sql_settings", []string{"isolation", "readonly"}, []string{"TEXT", "TEXT"}, [][]any{{mode.isolation, "off"}})
				run(settings)
				run(fixture.Prepared(settings))
				run(fixture.Exec("ROLLBACK TO SAVEPOINT external_sql_local_mark", 0))
				run(fixture.Exec("RELEASE SAVEPOINT external_sql_local_mark", 0))
				run(settings)
				run(fixture.Exec("ROLLBACK", 0))
				count(1, 1) // Raw committed work is not enlisted into local rollback.
			}
			if b.PG {
				return
			}
			handler.action = func(ctx context.Context, _ stuntgres.ForeignRequest, connection stuntgres.ExternalSQLConnection) error {
				_, err := connection.Execute(ctx, "INSERT INTO external_sql_source.marks VALUES(9)")
				return err
			}
			run(fixture.Exec("BEGIN", 0))
			read := externalSQLProviderRows(false, "SELECT id,label,flag,bytes FROM external_sql_data WHERE id=1", [][]any{{int64(1), "x", true, "\x00\xff"}})
			run(read)
			count(9, 0)
			run(fixture.Exec("SAVEPOINT effect", 0))
			run(fixture.Prepared(read))
			run(fixture.Exec("ROLLBACK TO effect", 0))
			run(fixture.Exec("COMMIT", 0))
			count(9, 1)
			for _, wire := range provider.snapshot()[1:] {
				externalSQLBackendGone(t, p.Ctx, wire.info.BackendPID)
			}

			// A separate pool shares the real connector but retains no idle
			// physical sessions: Close of its borrower must join remote rollback.
			db, recorder := fixture.RecordedNativePool(t, p.NativeConnector)
			db.SetMaxIdleConns(0)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			dedicated, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					_ = dedicated.Close()
				}
			})
			fixture.RunStep(t, p.Ctx, dedicated, recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, dedicated, recorder, fixture.Exec("DECLARE external_sql_close NO SCROLL CURSOR FOR SELECT id,label,flag,bytes FROM external_sql_data", 0))
			fetch := externalSQLProviderRows(false, "FETCH 1 FROM external_sql_close", [][]any{{int64(1), "x", true, "\x00\xff"}})
			fixture.RunStep(t, p.Ctx, dedicated, recorder, fetch)
			wires := provider.snapshot()
			pid := wires[len(wires)-1].info.BackendPID
			count(9, 1)
			if err := dedicated.Close(); err != nil {
				t.Fatal("physical close did not join cleanup", err)
			}
			closed = true
			externalSQLBackendGone(t, p.Ctx, pid)
			count(9, 1)
			handler.action = nil
		})
	}
}
