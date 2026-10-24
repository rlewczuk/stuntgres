package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type transactionalViewsFailedReader struct{ err error }

func (r transactionalViewsFailedReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceTransactionalViewsNativeControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			var emitted []byte
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE TABLE txv_source(id INT,txt TEXT)", 0),
						fixture.Exec("INSERT INTO txv_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3),
						fixture.Exec("CREATE VIEW txv_v AS SELECT id,txt FROM txv_source", 0),
						fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id,txt FROM txv_source", 3),
						fixture.Exec("CREATE MATERIALIZED VIEW txv_nodata AS SELECT id FROM txv_source WITH NO DATA", 0),
						fixture.Exec("INSERT INTO txv_source VALUES(3,'c')", 1),
					} {
						run(s)
					}
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("INSERT INTO txv_source VALUES(4,'private')", 1))
					run(fixture.Exec("CREATE VIEW txv_private AS SELECT id,txt FROM txv_source", 0))
					run(fixture.Exec("CREATE MATERIALIZED VIEW txv_private_m AS SELECT id,txt FROM txv_private", 5))
					run(fixture.Exec("CREATE TEMP VIEW txv_temp AS SELECT 1 AS id", 0))
					run(fixture.Exec("SAVEPOINT owner_mark", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsBag("txv_source", true))
					if b.PG {
						run(fixture.Exec("ROLLBACK", 0))
						if emitted != nil {
							run(fixture.Exec("DROP VIEW txv_v", 0))
							run(fixture.Exec("DROP MATERIALIZED VIEW txv_m,txv_nodata", 0))
							run(fixture.Exec("DROP TABLE txv_source", 0))
							// pq reports the complete dump message's terminal single-row
							// INSERT count, not the preceding definition-utility count.
							run(fixture.Exec(string(emitted), 1))
							run(transactionalViewsBag("txv_source", true))
							run(transactionalViewsBag("txv_v", true))
							// Native stored-result payload is not PostgreSQL SQL syntax.
							run(fixture.MaterializedViewUnpopulated("txv_m"))
							run(fixture.MaterializedViewUnpopulated("txv_nodata"))
						}
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = bytes.Clone(dump.Bytes())
					if strings.Contains(dump.String(), "txv_private") || strings.Contains(dump.String(), "txv_temp") || strings.Contains(dump.String(), "'private'") {
						t.Fatal("Dump exposed uncommitted or temporary state", dump.String())
					}
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
					if err != nil {
						t.Fatal(err)
					}
					clone := sql.OpenDB(connector)
					defer clone.Close()
					recordedClone, cloneRecorder := fixture.RecordedNativePool(t, connector)
					if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(emitted)); err != nil {
						t.Fatal(err)
					}
					crun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recordedClone, cloneRecorder, s) }
					crun(transactionalViewsBag("txv_source", true))
					crun(transactionalViewsBag("txv_v", true))
					crun(transactionalViewsBag("txv_m", false))
					fixture.MaterializedViewPopulation(crun, "txv_m", true)
					fixture.MaterializedViewPopulation(crun, "txv_nodata", false)
					crun(fixture.MaterializedViewUnpopulated("txv_nodata"))
					readErr := errors.New("transactional view restore reader failure")
					if err := stuntgres.Restore(p.Ctx, p.Native, transactionalViewsFailedReader{readErr}); !errors.Is(err, readErr) {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE txv_late(id INT); CREATE VIEW txv_bad AS SELECT absent FROM txv_late;")); err == nil {
						t.Fatal("invalid Restore accepted")
					}
					run(fixture.Select("SELECT count(*) FROM txv_private_m", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
					rows, err := c.QueryContext(p.Ctx, "SELECT id,txt FROM txv_private_m ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					stmt, err := c.PrepareContext(p.Ctx, "CREATE VIEW txv_pending AS SELECT id FROM txv_source")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted))
					}
					if err != nil {
						t.Fatal(err)
					}
					after, err := rows.Columns()
					if err != nil || !reflect.DeepEqual(columns, after) || !reflect.DeepEqual(columns, []string{"id", "txt"}) {
						t.Fatal(columns, after, err)
					}
					var got [][]any
					for rows.Next() {
						var id int64
						var txt any
						if err := rows.Scan(&id, &txt); err != nil {
							t.Fatal(err)
						}
						got = append(got, []any{id, txt})
					}
					want := [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}, {int64(4), "private"}}
					if rows.Err() != nil || !reflect.DeepEqual(got, want) {
						t.Fatal(got, rows.Err())
					}
					rows.Close()
					_, err = stmt.ExecContext(p.Ctx)
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					for _, q := range []string{"ROLLBACK TO owner_mark", "COMMIT"} {
						_, err = c.ExecContext(p.Ctx, q)
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					}
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Exec("CREATE VIEW txv_reused AS SELECT 8 AS id", 0))
					run(fixture.Select("SELECT id FROM txv_reused", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
				})
			}
		})
	}
}
