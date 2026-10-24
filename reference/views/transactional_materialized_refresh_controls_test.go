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

func TestReferenceTransactionalMaterializedRefreshControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			txRefreshRoutes(t, []string{"sql"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
				base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				txRefreshSeed(base)
				// Stored result deliberately differs from its producer at serialization.
				base(fixture.Exec("INSERT INTO txr_source VALUES(3)", 1))
				base(fixture.Exec("CREATE MATERIALIZED VIEW txr_empty AS SELECT i FROM txr_source WITH NO DATA", 0))
				q := txRefreshSQL(concurrent)
				route := txRefreshHeld(t, p, c, held, q)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				run(fixture.Exec("INSERT INTO txr_source VALUES(2)", 1))
				fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
				run(txRefreshRows("txr_mv", 1, 2, 3))
				run(fixture.Exec("SAVEPOINT controls", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, txRefreshRows("txr_source", 1, 3))
				if concurrent {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, txRefreshRows("txr_mv", 1))
				}
				if b.PG {
					finish(false)
					base(txRefreshRows("txr_mv", 1))
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
				if err != nil {
					t.Fatal(err)
				}
				clone := sql.OpenDB(connector)
				defer clone.Close()
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				crun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
				crun(txRefreshRows("txr_source", 1, 3))
				crun(txRefreshRows("txr_mv", 1))
				crun(fixture.MaterializedViewUnpopulated("txr_empty"))
				crun(fixture.Select("SELECT indexdef FROM pg_indexes WHERE indexname='txr_u'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX txr_u ON public.txr_mv USING btree (i)"}}))
				crun(fixture.Exec(q, 0))
				crun(txRefreshRows("txr_mv", 1, 3))
				failed := errors.New("refresh Restore reader failure")
				if err := stuntgres.Restore(p.Ctx, p.Native, transactionalViewsFailedReader{failed}); !errors.Is(err, failed) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE txr_late(i INT); INSERT INTO txr_late VALUES(1); SELECT absent FROM txr_late;")); err == nil {
					t.Fatal("late invalid Restore accepted")
				}
				fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
				run(txRefreshRows("txr_mv", 1, 2, 3))
				rows, err := c.QueryContext(p.Ctx, "SELECT i FROM txr_mv ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if replacement == "reset" {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				after, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(columns, after) || !reflect.DeepEqual(columns, []string{"i"}) || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal(columns, after, types, err)
				}
				var values []int64
				for rows.Next() {
					var i int64
					if err := rows.Scan(&i); err != nil {
						t.Fatal(err)
					}
					values = append(values, i)
				}
				if rows.Err() != nil || !reflect.DeepEqual(values, []int64{1, 2, 3}) {
					t.Fatal("retained eager result", values, rows.Err())
				}
				rows.Close()
				_, err = route(e).ExecContext(p.Ctx, q)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				for _, q := range []string{"ROLLBACK TO controls", "COMMIT"} {
					_, err = c.ExecContext(p.Ctx, q)
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				}
				base(fixture.Exec("ROLLBACK", 0))
				base(fixture.Select("SELECT 42 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
				if replacement == "restore" {
					base(txRefreshRows("txr_source", 1, 3))
					base(txRefreshRows("txr_mv", 1))
					base(fixture.Exec(q, 0))
					base(txRefreshRows("txr_mv", 1, 3))
				}
			})
		})
	}
}
