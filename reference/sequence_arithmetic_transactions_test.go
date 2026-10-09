package reference_test

import (
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticTransactions(t *testing.T) {
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
			exec("CREATE SEQUENCE frozen_s START 3", 0)
			value("SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 3)
			exec("CREATE TABLE dst(v BIGINT)", 0)
			// Restart is transactional; ordinary nextval/setval arithmetic remains nonrollback.
			exec("BEGIN", 0)
			exec("ALTER SEQUENCE frozen_s RESTART 100", 0)
			exec("INSERT INTO dst SELECT 0+nextval('frozen_s') FROM src WHERE id=1", 1)
			exec("ROLLBACK", 0)
			query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
			value("SELECT 0+currval('frozen_s') FROM src WHERE id=1", 100)
			value("SELECT 0+nextval('frozen_s') FROM src WHERE id=1", 4)
			exec("BEGIN", 0)
			value("SELECT setval('frozen_s',40+2,true)+0 FROM src WHERE id=1", 42)
			exec("INSERT INTO dst SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 1)
			exec("ROLLBACK", 0)
			query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
			value("SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 44)
			exec("BEGIN", 0)
			exec("ALTER SEQUENCE frozen_s RESTART 200", 0)
			exec("INSERT INTO dst SELECT nextval('frozen_s')+0 FROM src WHERE id=1", 1)
			exec("COMMIT", 0)
			query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, [][]any{{int64(200)}})
			value("SELECT 0+nextval('frozen_s') FROM src WHERE id=1", 201)
		})
	}
}
