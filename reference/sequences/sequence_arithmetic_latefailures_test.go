package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticLateFailures(t *testing.T) {
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
			// Failure is row-owned, after one demanded value; neither consumer publishes partial rows.
			exec("CREATE TABLE divisor(v BIGINT)", 0)
			exec("INSERT INTO divisor VALUES(0)", 1)
			exec("CREATE TABLE dst(v BIGINT)", 0)
			exec("CREATE SEQUENCE late_s", 0)
			fixture.RunStep(t, ctx, c, backend.Recorder, fixture.ErrorDiagnostic("INSERT INTO dst SELECT nextval('late_s')/v FROM divisor", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
			query("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
			value("SELECT currval('late_s')+0 FROM src WHERE id=1", 1)
			exec("CREATE TABLE doomed(v BIGINT)", 0)
			exec("INSERT INTO doomed VALUES(9223372036854775807)", 1)
			fixture.RunStep(t, ctx, c, backend.Recorder, fixture.ErrorDiagnostic("DELETE FROM doomed WHERE v+nextval('late_s')>0", fixture.ControlsReferenceDiagnostic("22003", "bigint out of range")))
			query("SELECT v FROM doomed", []string{"v"}, []string{"INT8"}, [][]any{{int64(9223372036854775807)}})
			value("SELECT currval('late_s')+0 FROM src WHERE id=1", 2)
		})
	}
}
