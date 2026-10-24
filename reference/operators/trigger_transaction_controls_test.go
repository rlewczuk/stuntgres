package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerControls(t *testing.T) {
	for _, replacement := range []string{"failed-restore-commit", "reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerDDLChain(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("INSERT INTO tr_data VALUES(1,5)", 1))
				q := "INSERT INTO tr_data VALUES($1,5) RETURNING id,v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(context.Background(), "ROLLBACK")
				run(fixture.Exec("SAVEPOINT original", 0))
				run(fixture.Exec("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)", 0))
				run(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(24)}))
				run(fixture.Exec(`ALTER TRIGGER z ON tr_data RENAME TO "0double"`, 0))
				run(triggerRows("INSERT INTO tr_data VALUES(3,5) RETURNING id,v", []any{int64(3), int64(17)}))
				run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
				run(triggerRows("INSERT INTO tr_data VALUES(4,5) RETURNING id,v", []any{int64(4), int64(10)}))
				run(fixture.Exec("CREATE TRIGGER b BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(1)", 0))
				run(fixture.Exec("ALTER TRIGGER b ON tr_data DEPENDS ON EXTENSION unaccent", 0))
				run(triggerRows("INSERT INTO tr_data VALUES(5,5) RETURNING id,v", []any{int64(5), int64(11)}))
				if b.PG {
					run(fixture.Exec("ROLLBACK", 0))
					run(triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(16)}))
					run(triggerRows("INSERT INTO tr_data VALUES(99,5) RETURNING id,v", []any{int64(99), int64(16)}))
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/dump-replay")
				if err != nil {
					t.Fatal(err)
				}
				clone := sql.OpenDB(connector)
				defer clone.Close()
				if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				if p.Postgres != nil {
					t.Run("committed-dump-postgres", func(t *testing.T) {
						pg, err := p.Postgres.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pg.Close()
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							if _, err := pg.ExecContext(ctx, "SET check_function_bodies=true; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Error(err)
							}
						}()
						if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal("unchanged committed trigger dump replay", err)
						}
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(16)}))
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, triggerDDLX("tr_data", "unaccent", 1))
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, triggerRows("INSERT INTO tr_data VALUES(99,5) RETURNING id,v", []any{int64(99), int64(16)}))
					})
				}
				recordedClone, cloneRecorder := fixture.RecordedNativePool(t, connector)
				cloneRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recordedClone, cloneRecorder, s) }
				cloneRun(triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(16)}))
				cloneRun(triggerDDLX("tr_data", "unaccent", 1))
				cloneRun(triggerRows("INSERT INTO tr_data VALUES(99,5) RETURNING id,v", []any{int64(99), int64(16)}))
				if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}); !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal(err)
				}
				for _, bad := range []string{dump.String() + "; INSERT INTO tr_data VALUES(90,'bad');", dump.String() + `; CREATE TRIGGER "unterminated`} {
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
						t.Fatal("bad restore accepted")
					}
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				run(triggerRows("SELECT id,v FROM tr_data WHERE id=3", []any{int64(3), int64(17)}))
				if replacement == "failed-restore-commit" {
					run(fixture.Exec("COMMIT", 0))
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(16)}, []any{int64(2), int64(24)}, []any{int64(3), int64(17)}, []any{int64(4), int64(10)}, []any{int64(5), int64(11)}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(99,5) RETURNING id,v", []any{int64(99), int64(11)}))
					return
				}
				retained, err := c.QueryContext(p.Ctx, "SELECT id,v FROM tr_data WHERE id=3")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				columns, err := retained.Columns()
				if err != nil || len(columns) != 2 || columns[0] != "id" || columns[1] != "v" {
					t.Fatal(columns, err)
				}
				types, err := retained.ColumnTypes()
				if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "INT4" {
					t.Fatal(types, err)
				}
				if replacement == "reset" {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				if !retained.Next() {
					t.Fatal("retained row lost", retained.Err())
				}
				var id, value int64
				if err := retained.Scan(&id, &value); err != nil || id != 3 || value != 17 {
					t.Fatal(id, value, err)
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal("retained cardinality", retained.Err())
				}
				retained.Close()
				_, err = stmt.QueryContext(p.Ctx, 99)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = c.ExecContext(p.Ctx, "ROLLBACK TO original")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = c.ExecContext(p.Ctx, "COMMIT")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				run(fixture.Exec("ROLLBACK", 0))
				if replacement == "reset" {
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				run(triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(16)}))
				s := triggerRows(q, []any{int64(99), int64(16)})
				s.Args = []any{99}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			})
		})
	}
}
