package reference_test

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestReferenceQueryTiesReplacement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, mode := range []string{"hidden computed", "visible computed", "physical slot"} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
					ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
					defer cancel()
					a, readerConn := referenceLockConnections(t, ctx, b.db)
					referenceLockExec(t, ctx, a, "CREATE TABLE ties_replace_t(id INT,k INT)", 0)
					referenceLockExec(t, ctx, a, "INSERT INTO ties_replace_t VALUES(1,1),(2,2),(3,3),(4,4)", 4)
					referenceLockExec(t, ctx, a, "CREATE SEQUENCE ties_replace_gate START WITH 1", 0)
					writer, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer writer.Rollback()
					referenceLockExec(t, ctx, writer, "UPDATE ties_replace_t SET k=2 WHERE id=3", 1)
					reader, err := readerConn.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					projection, key := "id", "k+0"
					if mode == "visible computed" {
						projection = "id,k+0 AS key"
						key = "key"
					}
					if mode == "physical slot" {
						key = "k"
					}
					q := "SELECT " + projection + " FROM ties_replace_t WHERE nextval('ties_replace_gate')>0 ORDER BY " + key + " FETCH FIRST 2 ROWS WITH TIES FOR UPDATE"
					var holderPID, readerPID int
					if b.pg {
						holderPID, readerPID = referenceRowPIDs(t, ctx, a, readerConn)
					}
					done := referenceRowAsync(t, ctx, reader, q, prepared, true)
					if b.pg {
						referenceRowWait(t, ctx, readerPID, holderPID)
					} else {
						// All four original source tuples have passed qualification.
						// Thus the sorter uses the old query snapshot even if the
						// writer commits just before the native lock arbitration.
						for {
							var value int64
							var called bool
							if err := a.QueryRowContext(ctx, "SELECT last_value,is_called FROM ties_replace_gate").Scan(&value, &called); err != nil {
								t.Fatal(err)
							}
							if called && value >= 4 {
								break
							}
							if ctx.Err() != nil {
								t.Fatal(ctx.Err())
							}
							runtime.Gosched()
						}
					}
					if err := writer.Commit(); err != nil {
						t.Fatal(err)
					}
					out := referenceLockResult(t, done)
					if out.stmt != nil {
						defer out.stmt.Close()
					}
					if out.err != nil {
						t.Fatal(out.err)
					}
					defer out.rows.Close()
					columns, err := out.rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					wantColumns := []string{"id"}
					if mode == "visible computed" {
						wantColumns = []string{"id", "key"}
					}
					if !reflect.DeepEqual(columns, wantColumns) {
						t.Fatal(columns, wantColumns)
					}
					meta, err := out.rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, column := range meta {
						if column.DatabaseTypeName() != "INT4" {
							t.Fatal(column)
						}
					}
					var ids []int64
					for out.rows.Next() {
						var id, keyValue int64
						if mode == "visible computed" {
							if err := out.rows.Scan(&id, &keyValue); err != nil {
								t.Fatal(err)
							}
							wantKey := int64(2)
							if id == 1 {
								wantKey = 1
							}
							if keyValue != wantKey {
								t.Fatal("replacement key", id, keyValue, wantKey)
							}
						} else if err := out.rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						ids = append(ids, id)
					}
					if out.rows.Err() != nil || !reflect.DeepEqual(ids, []int64{1, 2, 3}) {
						t.Fatal("replacement peers", ids, out.rows.Err())
					}
					if err := out.rows.Close(); err != nil {
						t.Fatal(err)
					}
					if err := reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceRunStep(t, ctx, b.db, b.recorder, referenceSelect("SELECT k FROM ties_replace_t ORDER BY id", []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 4)))
				})
			})
		}
	}
}
