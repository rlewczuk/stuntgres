package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceProducerUndemandedTransaction(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			conn := func() *sql.Conn {
				c, err := backend.db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				return c
			}
			c, b := conn(), conn()
			aSchema := fmt.Sprintf("producer_boundary_a_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_, _ = c.ExecContext(cleanup, "ROLLBACK")
				_, _ = c.ExecContext(cleanup, "SET search_path TO public")
				_, _ = c.ExecContext(cleanup, "DROP SCHEMA "+aSchema+" CASCADE")
			})
			run := func(step referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, step) }
			exec := func(q string) { run(referenceStep{query: q}) }
			rows := func(q string, columns, types []string, want [][]any) {
				run(referenceStep{query: q, selectRows: true, columns: columns, types: types, rows: want})
			}
			state := func(name string, value int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}})
			}
			exec("CREATE SCHEMA " + aSchema)
			exec("SET search_path TO " + aSchema)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
				exec(q)
			}
			run(referenceStep{query: "INSERT INTO src VALUES(1)", affected: 1})
			for _, prepared := range []bool{false, true} {
				exec("BEGIN")
				run(referenceStep{query: "INSERT INTO dst SELECT nextval('s') FROM src WHERE id=99", prepared: prepared})
				// Source-table AS is deliberately retained; B does not touch src/dst.
				peer, stop := context.WithTimeout(ctx, 2*time.Second)
				result, err := b.ExecContext(peer, "ALTER SEQUENCE "+aSchema+".s RESTART WITH 80")
				stop()
				if err != nil {
					t.Fatal("undemanded INSERT blocked peer RESTART", err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				exec("COMMIT")
				rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
				state("s", 80, false)
				run(sequenceInt64Step(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(80)}})))
			}
		})
	}
}
