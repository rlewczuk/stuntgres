package reference_test

import (
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticImports(t *testing.T) {
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
			exec("CREATE SEQUENCE nested START 10", 0)
			exec("CREATE SEQUENCE assigned", 0)
			value("SELECT 1+setval('assigned',nextval('nested')+5,false) FROM src WHERE id=1", 16)
			value("SELECT nextval('assigned')+nextval('nested') FROM src WHERE id=1", 26)
			// Imported conversions beneath nested arithmetic beat volatile effects even without rows.
			for _, source := range []string{"empty_src", "src WHERE id=99", "src WHERE id=1"} {
				failure("SELECT nextval('nested')+setval('assigned',($1::text::bigint)+1) FROM "+source,
					stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}, "bad")
				failure("SELECT nextval('nested')+setval($1,nextval('assigned')+1) FROM "+source,
					stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}, "missing")
			}
			value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
			value("SELECT currval('assigned')+0 FROM src WHERE id=1", 15)
		})
	}
}
