package reference_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteRetainedGroup(t *testing.T) {
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
			bSchema := aSchema + "_b"
			defer func() {
				_, _ = c.ExecContext(p.Ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.Ctx, "SET search_path TO public")
				for _, schema := range []string{aSchema, bSchema} {
					if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
			}()
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			exec := func(q string, n int64) { run(fixture.Exec(q, n)) }
			rows := func(q string, columns, types []string, want [][]any) { run(fixture.Select(q, columns, types, want)) }
			state := func(name string, n int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}})
			}
			exec("CREATE SCHEMA "+aSchema, 0)
			exec("CREATE SCHEMA "+bSchema, 0)
			// Same handle, complete two-literal source group, and imported parameter.
			for _, q := range []string{"CREATE SEQUENCE " + bSchema + ".x START 10 CACHE 1", "CREATE SEQUENCE " + bSchema + ".y START 20 CACHE 1", "CREATE TABLE " + bSchema + ".held(id INT)", "SET search_path TO " + aSchema + "," + bSchema} {
				exec(q, 0)
			}
			exec("INSERT INTO "+bSchema+".held VALUES(7)", 1)
			q := "DELETE FROM " + bSchema + ".held WHERE setval('x',$1::bigint)>0 AND nextval('y')>0"
			s, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("analysis-only DELETE Prepare", err)
			}
			defer s.Close()
			state(bSchema+".x", 10, false)
			state(bSchema+".y", 20, false)
			fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, backend.Recorder, fixture.Step{Query: q, Args: []any{int64(10)}, Affected: 1})
			rows("SELECT id FROM "+bSchema+".held", []string{"id"}, []string{"INT4"}, nil)
			exec("INSERT INTO "+bSchema+".held VALUES(7)", 1)
			exec("CREATE SEQUENCE "+aSchema+".x START 100 CACHE 1", 0)
			exec("ALTER SEQUENCE "+bSchema+".y RENAME TO identity", 0)
			failed := func() {
				want := fixture.ControlsReferenceDiagnostic("42P01", `relation "y" does not exist`)
				want.Position = strconv.Itoa(strings.Index(q, "'y'") + 1)
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, backend.Recorder, fixture.Step{Query: q, Args: []any{int64(150)}, State: want.Code, Diagnostic: &want, ErrorStage: "execute"})
				rows("SELECT id FROM "+bSchema+".held", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			failed()
			state(aSchema+".x", 100, false)
			state(bSchema+".x", 10, true)
			state(bSchema+".identity", 20, true)
			run(fixture.SequenceInt64Step(fixture.Select("SELECT currval('"+bSchema+".x')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(10)}})))
			run(fixture.SequenceInt64Step(fixture.Select("SELECT currval('"+bSchema+".identity')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(20)}})))
			run(fixture.SequenceInt64Step(fixture.Select("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{int64(20)}})))
			undefinedShadow := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "x" is not yet defined in this session`)
			run(fixture.Step{Query: "SELECT currval('" + aSchema + ".x')", SelectRows: true, State: undefinedShadow.Code, Diagnostic: &undefinedShadow, ErrorStage: "execute"})
			exec("CREATE SEQUENCE "+aSchema+".y START 200 CACHE 1", 0)
			deleted := func(n int64) {
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, backend.Recorder, fixture.Step{Query: q, Args: []any{n}, Affected: 1})
				rows("SELECT id FROM "+bSchema+".held", []string{"id"}, []string{"INT4"}, nil)
			}
			deleted(150)
			exec("INSERT INTO "+bSchema+".held VALUES(7)", 1)
			exec("SET search_path TO "+bSchema, 0)
			failed()
			state(aSchema+".x", 150, true)
			state(aSchema+".y", 200, true)
			state(bSchema+".x", 10, true)
			state(bSchema+".identity", 20, true)
			run(fixture.SequenceInt64Step(fixture.Select("SELECT currval('"+aSchema+".x')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(150)}})))
			run(fixture.SequenceInt64Step(fixture.Select("SELECT currval('"+aSchema+".y')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(200)}})))
			run(fixture.SequenceInt64Step(fixture.Select("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{int64(200)}})))
			exec("SET search_path TO "+aSchema+","+bSchema, 0)
			deleted(151)
			state(aSchema+".x", 151, true)
			state(aSchema+".y", 201, true)
		})
	}
}
