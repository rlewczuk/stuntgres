package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteWrongKindAndProducer(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			defer cancel()
			c, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			aSchema := fmt.Sprintf("delete_boundary_%d", time.Now().UnixNano())
			defer func() {
				_, _ = c.ExecContext(p.Ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.Ctx, "SET search_path TO public")
				if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA "+aSchema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			exec := func(q string, n int64) { run(fixture.Exec(q, n)) }
			rows := func(q string, columns, types []string, want [][]any) { run(fixture.Select(q, columns, types, want)) }
			state := func(name string, n int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}})
			}
			exec("CREATE SCHEMA "+aSchema, 0)
			exec("SET search_path TO "+aSchema, 0)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE TABLE t(id INT)"} {
				exec(q, 0)
			}
			exec("INSERT INTO t VALUES(1),(1),(2)", 3)
			exec("ALTER SEQUENCE s RESTART 80", 0)
			exec("SET search_path TO "+aSchema, 0)
			for _, prepared := range []bool{false, true} {
				want := fixture.ControlsReferenceDiagnostic("42809", `cannot change sequence "s"`)
				run(fixture.Step{Query: "DELETE FROM s WHERE nextval('s')>0", Prepared: prepared, State: want.Code, Diagnostic: &want, ErrorStage: "execute"})
				state("s", 80, false)
			}
			exec("DELETE FROM t WHERE id=2", 1)
			exec("CREATE SEQUENCE producer START 10 CACHE 1", 0)
			exec("CREATE TABLE dst(id INT,n BIGINT)", 0)
			q := "INSERT INTO dst SELECT id,nextval('producer') FROM t ORDER BY id"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			state("producer", 10, false)
			fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, backend.Recorder, fixture.Exec(q, 2))
			rows("SELECT id,n FROM dst ORDER BY n", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(11)}})
			run(fixture.SequenceInt64Step(fixture.Select("SELECT nextval('producer')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(12)}})))
		})
	}
}
