package reference_test

import (
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticStableMemory(t *testing.T) {
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
			failure := func(q string, want stuntgres.Error, args ...any) {
				expected := fixture.ControlsReferenceDiagnostic(want.Code, want.Message)
				expected.Position, expected.Context = want.Position, want.Context
				for _, prepared := range []bool{false, true} {
					step := fixture.Select(q, nil, nil, nil, args...)
					step.Prepared = prepared
					step.State = want.Code
					step.Diagnostic = &expected
					step.ErrorStage = "execute"
					fixture.RunStep(t, ctx, c, backend.Recorder, step)
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
