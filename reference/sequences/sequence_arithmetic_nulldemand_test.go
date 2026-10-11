package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticNullDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			c := fixture.SequenceCoreConn(t, ctx, backend.DB)
			schema := fmt.Sprintf("seq_arith_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, _ = c.ExecContext(ctx, "ROLLBACK")
				_, _ = c.ExecContext(ctx, "SET search_path TO public")
				_, _ = c.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
			})
			exec := func(q string, n int64) { fixture.RunStep(t, ctx, c, backend.Recorder, fixture.Exec(q, n)) }
			query := func(q string, labels, types []string, want [][]any, args ...any) {
				fixture.RunStep(t, ctx, c, backend.Recorder, sequenceArithmeticNullInt64Step(fixture.Select(q, labels, types, want, args...)))
			}
			value := func(q string, n int64) { query(q, []string{"?column?"}, []string{"INT8"}, [][]any{{n}}) }
			exec("CREATE SCHEMA "+schema, 0)
			exec("SET search_path TO "+schema, 0)
			exec("CREATE TABLE src(id INT, nullable BIGINT)", 0)
			exec("INSERT INTO src VALUES(1,NULL),(2,7)", 2)
			exec("CREATE SEQUENCE nested START 10", 0)
			exec("CREATE SEQUENCE assigned", 0)
			value("SELECT 1+setval('assigned',nextval('nested')+5,false) FROM src WHERE id=1", 16)
			value("SELECT nextval('assigned')+nextval('nested') FROM src WHERE id=1", 26)
			// Known NULL discards nested effects; row-derived NULL still evaluates all arguments.
			query("SELECT setval('assigned',nextval('nested')+1,NULL)+0 FROM src WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{nil}})
			value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
			query("SELECT setval('assigned',nextval('nested')+nullable,false)+0 FROM src WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{nil}})
			value("SELECT currval('nested')+0 FROM src WHERE id=1", 12)
			value("SELECT currval('assigned')+0 FROM src WHERE id=1", 15)
		})
	}
}
