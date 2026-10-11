package reference_test

import (
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticConsumerImports(t *testing.T) {
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
			exec("CREATE TABLE empty_src(id INT)", 0)
			exec("CREATE SEQUENCE nested START 10", 0)
			exec("CREATE SEQUENCE assigned", 0)
			value("SELECT 1+setval('assigned',nextval('nested')+5,false) FROM src WHERE id=1", 16)
			value("SELECT nextval('assigned')+nextval('nested') FROM src WHERE id=1", 26)
			exec("CREATE TABLE import_dst(v BIGINT)", 0)
			for _, source := range []string{"empty_src", "src WHERE id=99", "src WHERE id=1"} {
				for _, imported := range []struct {
					expression string
					arg        any
					want       stuntgres.Error
				}{
					{"nextval('nested')+setval('assigned',($1::text::bigint)+1)", "bad",
						stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`}},
					{"nextval('nested')+setval($1,nextval('assigned')+1)", "missing",
						stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"}},
				} {
					queries := []string{"INSERT INTO import_dst SELECT " + imported.expression + " FROM " + source}
					if source == "empty_src" {
						queries = append(queries, "DELETE FROM empty_src WHERE "+imported.expression+">0",
							"DELETE FROM src WHERE "+imported.expression+">0")
					}
					for _, q := range queries {
						expected := fixture.ControlsReferenceDiagnostic(imported.want.Code, imported.want.Message)
						expected.Context = imported.want.Context
						for _, prepared := range []bool{false, true} {
							fixture.RunStep(t, ctx, c, backend.Recorder, fixture.Step{Query: q, Args: []any{imported.arg}, Prepared: prepared, State: expected.Code, Diagnostic: &expected, ErrorStage: "execute"})
						}
					}
				}
			}
			query("SELECT v FROM import_dst", []string{"v"}, []string{"INT8"}, nil)
			query("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			value("SELECT currval('nested')+0 FROM src WHERE id=1", 11)
			value("SELECT currval('assigned')+0 FROM src WHERE id=1", 15)
		})
	}
}
