package reference_test

import (
	"fmt"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticImports(t *testing.T) {
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
