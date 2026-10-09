package reference_test

import (
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticLateFailures(t *testing.T) {
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
			// Failure is row-owned, after one demanded value; neither consumer publishes partial rows.
			exec("CREATE TABLE divisor(v BIGINT)", 0)
			exec("INSERT INTO divisor VALUES(0)", 1)
			exec("CREATE TABLE dst(v BIGINT)", 0)
			exec("CREATE SEQUENCE late_s", 0)
			referenceRunStep(t, ctx, c, backend.recorder, referenceErrorDiagnostic("INSERT INTO dst SELECT nextval('late_s')/v FROM divisor", controlsReferenceDiagnostic("22012", "division by zero")))
			query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
			value("SELECT currval('late_s')+0 FROM src WHERE id=1", 1)
			exec("CREATE TABLE doomed(v BIGINT)", 0)
			exec("INSERT INTO doomed VALUES(9223372036854775807)", 1)
			referenceRunStep(t, ctx, c, backend.recorder, referenceErrorDiagnostic("DELETE FROM doomed WHERE v+nextval('late_s')>0", controlsReferenceDiagnostic("22003", "bigint out of range")))
			query("SELECT v FROM doomed", []string{"v"}, []string{"INT8"}, [][]any{{int64(9223372036854775807)}})
			value("SELECT currval('late_s')+0 FROM src WHERE id=1", 2)
		})
	}
}
