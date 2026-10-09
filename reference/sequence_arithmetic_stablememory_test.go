package reference_test

import (
	"fmt"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticStableMemory(t *testing.T) {
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
			failure := func(q string, want stuntgres.Error, args ...any) {
				expected := controlsReferenceDiagnostic(want.Code, want.Message)
				expected.Position, expected.Context = want.Position, want.Context
				for _, prepared := range []bool{false, true} {
					step := referenceSelect(q, nil, nil, nil, args...)
					step.prepared = prepared
					step.state = want.Code
					step.diagnostic = &expected
					step.errorStage = "execute"
					referenceRunStep(t, ctx, c, backend.recorder, step)
				}
			}
			exec("CREATE SCHEMA "+schema, 0)
			exec("SET search_path TO "+schema, 0)
			exec("CREATE TABLE src(id INT, nullable BIGINT)", 0)
			exec("INSERT INTO src VALUES(1,NULL),(2,7)", 2)
			exec("CREATE TABLE empty_src(id INT)", 0)
			// Stable right children must also remain row-owned beneath a non-identity cast.
			exec("CREATE SEQUENCE stable_s", 0)
			for _, source := range []string{"empty_src", "src WHERE id=99"} {
				query("SELECT CAST(0+currval('stable_s') AS integer) FROM "+source, []string{"int4"}, []string{"INT4"}, nil)
			}
			failure("SELECT CAST(0+currval('stable_s') AS integer) FROM src WHERE id=1",
				stuntgres.Error{Code: "55000", Message: `currval of sequence "stable_s" is not yet defined in this session`})
			value("SELECT nextval('stable_s')+0 FROM src WHERE id=1", 1)
			query("SELECT CAST(0+currval('stable_s') AS integer),CAST(0+lastval() AS integer) FROM src ORDER BY id",
				[]string{"int4", "int4"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}})
		})
	}
}
