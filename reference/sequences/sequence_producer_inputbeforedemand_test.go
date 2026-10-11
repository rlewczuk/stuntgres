package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceProducerInputBeforeDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			defer cancel()
			c := fixture.SequenceCoreConn(t, ctx, backend.DB)
			aSchema := fmt.Sprintf("producer_boundary_a_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_, _ = c.ExecContext(cleanup, "ROLLBACK")
				_, _ = c.ExecContext(cleanup, "SET search_path TO public")
				_, _ = c.ExecContext(cleanup, "DROP SCHEMA "+aSchema+" CASCADE")
			})
			run := func(step fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, step) }
			exec := func(q string) { run(fixture.Step{Query: q}) }
			rows := func(q string, columns, types []string, want [][]any) {
				run(fixture.Step{Query: q, SelectRows: true, Columns: columns, Types: types, Rows: want})
			}
			state := func(name string, value int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}})
			}
			exec("CREATE SCHEMA " + aSchema)
			exec("SET search_path TO " + aSchema)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_src(id INT)", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
				exec(q)
			}
			run(fixture.Step{Query: "INSERT INTO src VALUES(1)", Affected: 1})
			bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			missingInput := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			missingInput.Context = "unnamed portal parameter $1 = '...'"
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"empty_src", "src WHERE id=99", "src"} {
					for _, tc := range []struct {
						q    string
						arg  any
						diag fixture.Diagnostic
					}{
						{"INSERT INTO dst SELECT setval('s',$1::text::bigint) FROM " + source, "bad", bad},
						{"INSERT INTO dst SELECT setval($1,nextval('nested')) FROM " + source, "missing", missingInput},
					} {
						run(fixture.Step{Query: tc.q, Args: []any{tc.arg}, Prepared: prepared, State: tc.diag.Code, Diagnostic: &tc.diag, ErrorStage: "execute"})
						rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
						state("s", 31, false)
						state("nested", 47, false)
						for _, name := range []string{"s", "nested"} {
							want := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "`+name+`" is not yet defined in this session`)
							run(fixture.Step{Query: "SELECT currval('" + name + "')", SelectRows: true, State: "55000", Diagnostic: &want, ErrorStage: "execute"})
						}
						want := fixture.ControlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
						run(fixture.Step{Query: "SELECT lastval()", SelectRows: true, State: "55000", Diagnostic: &want, ErrorStage: "execute"})
					}
				}
			}
			run(fixture.SequenceInt64Step(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(31)}})))
			run(fixture.SequenceInt64Step(fixture.Select("SELECT nextval('nested')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(47)}})))
		})
	}
}
