package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticLiteralGroup(t *testing.T) {
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
			// Whole retained literal group fails before the earlier arithmetic sibling can allocate.
			exec("CREATE SEQUENCE first_s START 30", 0)
			exec("CREATE SEQUENCE second_s START 70", 0)
			q := "SELECT nextval('first_s')+nextval('second_s') FROM src WHERE id=1"
			s, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			exec("ALTER SEQUENCE second_s RENAME TO renamed_s", 0)
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "second_s" does not exist`)
			want.Position = "35"
			step := fixture.ErrorDiagnostic(q, want)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, backend.Recorder, step)
			value("SELECT nextval('first_s')+0 FROM src WHERE id=1", 30)
			value("SELECT nextval('renamed_s')+0 FROM src WHERE id=1", 70)
		})
	}
}
