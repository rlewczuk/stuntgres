package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteUndemandedTransaction(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			b, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			aSchema := fmt.Sprintf("delete_boundary_%d", time.Now().UnixNano())
			defer func() {
				_, _ = c.ExecContext(p.ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.ctx, "SET search_path TO public")
				if _, err := c.ExecContext(p.ctx, "DROP SCHEMA "+aSchema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, ctx, c, backend.recorder, s) }
			exec := func(q string, n int64) { run(referenceExec(q, n)) }
			rows := func(q string, columns, types []string, want [][]any) { run(referenceSelect(q, columns, types, want)) }
			state := func(name string, n int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}})
			}
			exec("CREATE SCHEMA "+aSchema, 0)
			exec("SET search_path TO "+aSchema, 0)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)"} {
				exec(q, 0)
			}
			exec("INSERT INTO t VALUES(1),(1),(2)", 3)
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0", "DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
					exec("BEGIN", 0)
					run(referenceStep{query: q, prepared: prepared})
					peer, stop := context.WithTimeout(ctx, 2*time.Second)
					r, err := b.ExecContext(peer, "ALTER SEQUENCE "+aSchema+".s RESTART 80")
					stop()
					if err != nil {
						t.Fatal("undemanded DELETE blocked peer RESTART while initiating TX remains open", err)
					}
					if n, err := r.RowsAffected(); err != nil || n != 0 {
						t.Fatal(n, err)
					}
					exec("COMMIT", 0)
					state("s", 80, false)
					want := controlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)
					run(referenceStep{query: "SELECT currval('s')", selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
				}
			}
			rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
		})
	}
}
