package reference_test

import (
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticNestedEffects(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			c := sequenceCoreConn(t, ctx, backend.db)
			schema := fmt.Sprintf("seq_arith_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, _ = c.ExecContext(ctx, "ROLLBACK")
				_, _ = c.ExecContext(ctx, "SET search_path TO public")
				_, _ = c.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
			})
			exec := func(q string, n int64) { referenceRunStep(t, ctx, c, backend.recorder, referenceExec(q, n)) }
			query := func(q string, labels, types []string, want [][]any, args ...any) {
				referenceRunStep(t, ctx, c, backend.recorder, sequenceArithmeticNullInt64Step(referenceSelect(q, labels, types, want, args...)))
			}
			value := func(q string, n int64) { query(q, []string{"?column?"}, []string{"INT8"}, [][]any{{n}}) }
			exec("CREATE SCHEMA "+schema, 0)
			exec("SET search_path TO "+schema, 0)
			exec("CREATE TABLE src(id INT, nullable BIGINT)", 0)
			exec("INSERT INTO src VALUES(1,NULL),(2,7)", 2)
			// Ordinary nested arguments and setval's own effect each occur exactly once.
			exec("CREATE SEQUENCE nested START 10", 0)
			exec("CREATE SEQUENCE assigned", 0)
			value("SELECT 1+setval('assigned',nextval('nested')+5,false) FROM src WHERE id=1", 16)
			value("SELECT nextval('assigned')+nextval('nested') FROM src WHERE id=1", 26)
			value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
		})
	}
}
