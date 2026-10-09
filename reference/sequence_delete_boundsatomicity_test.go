package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteBoundsAtomicity(t *testing.T) {
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
			exec("CREATE TABLE t(id INT)", 0)
			exec("INSERT INTO t VALUES(1),(1),(2)", 3)
			for _, prepared := range []bool{false, true} {
				exec("CREATE SEQUENCE bounded MINVALUE 1 MAXVALUE 2 CACHE 1", 0)
				want := controlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (2)`)
				run(referenceStep{query: "DELETE FROM t WHERE nextval('bounded')>0", prepared: prepared, state: want.Code, diagnostic: &want, errorStage: "execute"})
				rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
				state("bounded", 2, true)
				run(sequenceInt64Step(referenceSelect("SELECT currval('bounded')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}})))
				run(sequenceInt64Step(referenceSelect("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{int64(2)}})))
				exec("DROP SEQUENCE bounded", 0)
			}
		})
	}
}
