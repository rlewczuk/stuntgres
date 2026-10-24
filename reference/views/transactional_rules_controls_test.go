package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesControls(t *testing.T) {
	for _, replacement := range []string{"failed-restores", "reset", "restore", "owner-close"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			var emitted []byte
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					base := transactionalRulesRun(t, p, b, c, nil)
					transactionalRulesSetup(base)
					base(fixture.Exec(transactionalRulesCreate, 0))
					base(fixture.Exec("CREATE RULE gone AS ON UPDATE TO src DO NOTHING", 0))
					base(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
					held := transactionalRulesHandles(t, p, c, true, transactionalRulesReplace)
					base(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run := transactionalRulesRun(t, p, b, c, held)
					run(fixture.Exec(transactionalRulesReplace, 0))
					run(fixture.Exec("DROP RULE gone ON src", 0))
					run(fixture.Exec("CREATE RULE added AS ON DELETE TO src DO NOTHING", 0))
					run(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
					run(fixture.Exec("SAVEPOINT owner_mark", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalRulesNames("gone", "r"))
					if b.PG {
						run(fixture.Exec("ROLLBACK", 0))
						if emitted != nil {
							run(fixture.Exec("DROP TABLE src,other,sink1,sink2 CASCADE", 0))
							run(fixture.Exec(string(emitted), 0))
							run(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
							run(transactionalRulesRows("sink1", []any{int64(1), int64(10)}))
							run(transactionalRulesRows("sink2"))
							run(fixture.Exec("INSERT INTO src VALUES(3,-30)", 1))
							run(transactionalRulesRows("sink1", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}))
						}
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = bytes.Clone(dump.Bytes())
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
					if err != nil {
						t.Fatal(err)
					}
					clone := sql.OpenDB(connector)
					defer clone.Close()
					recorded, rec := fixture.RecordedNativePool(t, connector)
					if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(emitted)); err != nil {
						t.Fatal(err)
					}
					crun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, rec, s) }
					crun(transactionalRulesNames("gone", "r"))
					crun(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
					crun(transactionalRulesRows("sink1", []any{int64(1), int64(10)}))
					crun(transactionalRulesRows("sink2"))
					crun(fixture.Exec("INSERT INTO src VALUES(3,-30)", 1))
					crun(transactionalRulesRows("sink1", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}))
					readerErr := errors.New("rule restore reader failed")
					if err := stuntgres.Restore(p.Ctx, p.Native, transactionalViewsFailedReader{readerErr}); !errors.Is(err, readerErr) {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE late(id INT); CREATE RULE broken AS ON INSERT TO late DO INSERT INTO absent VALUES(NEW.id);")); err == nil {
						t.Fatal("invalid late rule accepted")
					}
					run(transactionalRulesNames("added", "r"))
					run(fixture.Exec("INSERT INTO src VALUES(4,40)", 1))
					run(transactionalRulesRows("sink2", []any{int64(2), int64(21)}, []any{int64(4), int64(41)}))
					if replacement == "failed-restores" {
						run(fixture.Exec("COMMIT", 0))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalRulesNames("added", "r"))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalRulesRows("sink2", []any{int64(2), int64(21)}, []any{int64(4), int64(41)}))
						return
					}
					if replacement == "owner-close" {
						// Closing a sql.Conn only returns its lease. ErrBadConn
						// retires the physical owner through database/sql instead.
						if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Fatal(err)
						}
						if err := c.Close(); !errors.Is(err, sql.ErrConnDone) {
							t.Fatal(err)
						}
						// A fresh owner must see rollback and acquire the old event's locks.
						reuse, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer reuse.Close()
						rr := transactionalRulesRun(t, p, b, reuse, nil)
						rr(transactionalRulesNames("gone", "r"))
						rr(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
						rr(transactionalRulesRows("sink2"))
						rr(fixture.Exec("INSERT INTO src VALUES(5,-50)", 1))
						rr(transactionalRulesRows("sink1", []any{int64(1), int64(10)}, []any{int64(5), int64(50)}))
						return
					}
					rows, err := c.QueryContext(p.Ctx, "SELECT id,v FROM sink2 ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted))
					}
					if err != nil {
						t.Fatal(err)
					}
					after, err := rows.Columns()
					if err != nil || !reflect.DeepEqual(columns, []string{"id", "v"}) || !reflect.DeepEqual(columns, after) {
						t.Fatal(columns, after, err)
					}
					var got [][]any
					for rows.Next() {
						var id, v int64
						if err := rows.Scan(&id, &v); err != nil {
							t.Fatal(err)
						}
						got = append(got, []any{id, v})
					}
					if rows.Err() != nil || !reflect.DeepEqual(got, [][]any{{int64(2), int64(21)}, {int64(4), int64(41)}}) {
						t.Fatal(got, rows.Err())
					}
					rows.Close()
					_, err = held[transactionalRulesReplace].ExecContext(p.Ctx)
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					for _, q := range []string{"ROLLBACK TO owner_mark", "COMMIT"} {
						_, err = c.ExecContext(p.Ctx, q)
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					}
					base(fixture.Exec("ROLLBACK", 0))
					base(fixture.Exec("CREATE TABLE reused(id INT)", 0))
					base(fixture.Exec("INSERT INTO reused VALUES(7)", 1))
					base(fixture.Select("SELECT id FROM reused", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}
