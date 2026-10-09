package reference_test

import (
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticDemand(t *testing.T) {
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
			exec("CREATE TABLE empty_src(id INT)", 0)
			// Separate direction and dispatch cases catch right-child volatility lost under a cast.
			for _, side := range []string{"left", "right"} {
				for _, held := range []bool{false, true} {
					name := fmt.Sprintf("demand_%s_%t", side, held)
					exec("CREATE SEQUENCE "+name, 0)
					expression := "nextval('" + name + "')+0"
					if side == "right" {
						expression = "0+nextval('" + name + "')"
					}
					for _, source := range []string{"src WHERE id=99", "empty_src"} {
						q := "SELECT CAST(" + expression + " AS integer) FROM " + source
						if held {
							s, err := c.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal(q, err)
							}
							referenceRunStep(t, ctx, referenceHeldStatement{referenceExecutor: c, statement: s, query: q}, backend.recorder, sequenceArithmeticNullInt64Step(referenceSelect(q, []string{"int4"}, []string{"INT4"}, nil)))
							s.Close()
						} else {
							query(q, []string{"int4"}, []string{"INT4"}, nil)
						}
					}
					value("SELECT nextval('"+name+"')+0 FROM src WHERE id=1", 1)
					q := "SELECT CAST(" + expression + " AS integer) FROM src ORDER BY id"
					if held {
						s, err := c.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						referenceRunStep(t, ctx, referenceHeldStatement{referenceExecutor: c, statement: s, query: q}, backend.recorder, sequenceArithmeticNullInt64Step(referenceSelect(q, []string{"int4"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})))
						s.Close()
					} else {
						query(q, []string{"int4"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
					}
					value("SELECT 0+currval('"+name+"') FROM src WHERE id=1", 3)
				}
			}
		})
	}
}
