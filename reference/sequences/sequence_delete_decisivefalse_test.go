package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteDecisiveFalse(t *testing.T) {
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
			exec("CREATE TABLE t(id INT)", 0)
			exec("INSERT INTO t VALUES(1),(1),(2)", 3)
			// Assert the established backend distinction independently, not
			// parity: native evaluates both logical children, PG folds FALSE.
			exec("CREATE SEQUENCE decisive START 101 CACHE 1", 0)
			for i, prepared := range []bool{false, true} {
				exec("BEGIN", 0)
				run(fixture.Step{Query: "DELETE FROM t WHERE false AND nextval('decisive')>0", Prepared: prepared})
				if backend.Name == "memory" {
					want := int64(103 + i*3)
					state("decisive", want, true)
					run(fixture.SequenceInt64Step(fixture.Select("SELECT currval('decisive')", []string{"currval"}, []string{"INT8"}, [][]any{{want}})))
					run(fixture.SequenceInt64Step(fixture.Select("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{want}})))
				} else {
					state("decisive", 101, false)
					want := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "decisive" is not yet defined in this session`)
					// A diagnostic aborts an explicit PG transaction, so
					// inspect session memory only after ending the DELETE TX.
					exec("ROLLBACK", 0)
					run(fixture.Step{Query: "SELECT currval('decisive')", SelectRows: true, State: want.Code, Diagnostic: &want, ErrorStage: "execute"})
				}
				if backend.Name == "memory" {
					exec("ROLLBACK", 0)
				}
				rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
				if !backend.PG {
					state("decisive", int64(103+i*3), true)
				}
			}
		})
	}
}
