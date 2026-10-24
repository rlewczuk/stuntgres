package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceConversionTransactionControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE CONVERSION ctrl_base FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1`, 0))
			run(fixture.Exec("CREATE TABLE ctrl_committed(v integer)", 0))
			run(fixture.Exec("INSERT INTO ctrl_committed VALUES(7)", 1))
			use := fixture.Select(`SELECT convert_to('é','LATIN1') AS v`, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}})
			run(use)
			stmt, err := c.PrepareContext(p.Ctx, use.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT original", 0))
			run(fixture.Exec(`CREATE CONVERSION ctrl_private FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1`, 0))
			run(fixture.Exec("INSERT INTO ctrl_committed VALUES(9)", 1))
			run(fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='ctrl_private'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='ctrl_private'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if b.PG {
				run(fixture.Exec("ROLLBACK", 0))
				run(use)
				run(fixture.Select("SELECT v FROM ctrl_committed", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			cloneConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/recorded-clone")
			if err != nil {
				t.Fatal(err)
			}
			recorded, rec := fixture.RecordedNativePool(t, cloneConnector)
			raw := sql.OpenDB(cloneConnector)
			defer raw.Close()
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, s := range []fixture.Step{use, fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='ctrl_private'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}), fixture.Select("SELECT v FROM ctrl_committed", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})} {
				fixture.RunStep(t, p.Ctx, recorded, rec, s)
			}
			if p.Postgres != nil {
				pg := fixture.SequenceCoreConn(t, p.Ctx, p.Postgres)
				if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("unchanged SQL dump on PostgreSQL", err)
				}
				fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, use)
				fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='ctrl_private'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"; SELECT 1/0;")); err == nil {
				t.Fatal("late failed restore accepted")
			}
			run(fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='ctrl_private'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			retained, err := c.QueryContext(p.Ctx, "SELECT v FROM ctrl_committed ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceConsume(t, false, retained, nil, fixture.Select("SELECT v FROM ctrl_committed ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
			_, err = stmt.QueryContext(p.Ctx)
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			_, err = c.ExecContext(p.Ctx, "ROLLBACK TO original")
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			_, err = c.ExecContext(p.Ctx, "COMMIT")
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			run(fixture.Exec("ROLLBACK", 0))
			run(use)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: use.Query}, b.Recorder, use)
			run(fixture.Select(`SELECT count(*) AS n FROM pg_conversion WHERE conname='ctrl_private'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	}
}
