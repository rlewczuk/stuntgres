package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceProducerInputBeforeDemand(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			c := sequenceCoreConn(t, ctx, backend.db)
			aSchema := fmt.Sprintf("producer_boundary_a_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_, _ = c.ExecContext(cleanup, "ROLLBACK")
				_, _ = c.ExecContext(cleanup, "SET search_path TO public")
				_, _ = c.ExecContext(cleanup, "DROP SCHEMA "+aSchema+" CASCADE")
			})
			run := func(step referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, step) }
			exec := func(q string) { run(referenceStep{query: q}) }
			rows := func(q string, columns, types []string, want [][]any) {
				run(referenceStep{query: q, selectRows: true, columns: columns, types: types, rows: want})
			}
			state := func(name string, value int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}})
			}
			exec("CREATE SCHEMA " + aSchema)
			exec("SET search_path TO " + aSchema)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_src(id INT)", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
				exec(q)
			}
			run(referenceStep{query: "INSERT INTO src VALUES(1)", affected: 1})
			bad := controlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			missingInput := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			missingInput.Context = "unnamed portal parameter $1 = '...'"
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"empty_src", "src WHERE id=99", "src"} {
					for _, tc := range []struct {
						q    string
						arg  any
						diag referenceDiagnostic
					}{
						{"INSERT INTO dst SELECT setval('s',$1::text::bigint) FROM " + source, "bad", bad},
						{"INSERT INTO dst SELECT setval($1,nextval('nested')) FROM " + source, "missing", missingInput},
					} {
						run(referenceStep{query: tc.q, args: []any{tc.arg}, prepared: prepared, state: tc.diag.Code, diagnostic: &tc.diag, errorStage: "execute"})
						rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
						state("s", 31, false)
						state("nested", 47, false)
						for _, name := range []string{"s", "nested"} {
							want := controlsReferenceDiagnostic("55000", `currval of sequence "`+name+`" is not yet defined in this session`)
							run(referenceStep{query: "SELECT currval('" + name + "')", selectRows: true, state: "55000", diagnostic: &want, errorStage: "execute"})
						}
						want := controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
						run(referenceStep{query: "SELECT lastval()", selectRows: true, state: "55000", diagnostic: &want, errorStage: "execute"})
					}
				}
			}
			run(sequenceInt64Step(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(31)}})))
			run(sequenceInt64Step(referenceSelect("SELECT nextval('nested')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(47)}})))
		})
	}
}
