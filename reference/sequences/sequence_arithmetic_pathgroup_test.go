package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticPathGroup(t *testing.T) {
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
			// A changed search path invalidates the entire held arithmetic literal group.
			a, b := schema+"_path_a", schema+"_path_b"
			exec("CREATE SCHEMA "+a, 0)
			exec("CREATE SCHEMA "+b, 0)
			defer func() {
				_, _ = c.ExecContext(ctx, "DROP SCHEMA "+a+" CASCADE")
				_, _ = c.ExecContext(ctx, "DROP SCHEMA "+b+" CASCADE")
			}()
			exec("CREATE SEQUENCE "+a+".path_first START 10", 0)
			exec("CREATE SEQUENCE "+a+".path_second START 20", 0)
			exec("CREATE SEQUENCE "+b+".path_first START 100", 0)
			exec("SET search_path TO "+a+","+schema, 0)
			pathQuery := "SELECT nextval('path_first')+nextval('path_second') FROM src WHERE id=1"
			pathStmt, err := c.PrepareContext(ctx, pathQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer pathStmt.Close()
			exec("SET search_path TO "+b+","+schema, 0)
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "path_second" does not exist`)
			want.Position = "38"
			step := fixture.ErrorDiagnostic(pathQuery, want)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: pathStmt, SQL: pathQuery}, backend.Recorder, step)
			value("SELECT nextval('"+b+".path_first')+0 FROM src WHERE id=1", 100)
			exec("SET search_path TO "+a+","+schema, 0)
			fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: pathStmt, SQL: pathQuery}, backend.Recorder, sequenceArithmeticNullInt64Step(fixture.Select(pathQuery, []string{"?column?"}, []string{"INT8"}, [][]any{{int64(30)}})))
			value("SELECT currval('path_first')+0 FROM src WHERE id=1", 10)
			value("SELECT currval('path_second')+0 FROM src WHERE id=1", 20)
		})
	}
}
