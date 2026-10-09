package reference_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteRetainedGroup(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			aSchema := fmt.Sprintf("delete_boundary_%d", time.Now().UnixNano())
			bSchema := aSchema + "_b"
			defer func() {
				_, _ = c.ExecContext(p.ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.ctx, "SET search_path TO public")
				for _, schema := range []string{aSchema, bSchema} {
					if _, err := c.ExecContext(p.ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
			}()
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, ctx, c, backend.recorder, s) }
			exec := func(q string, n int64) { run(referenceExec(q, n)) }
			rows := func(q string, columns, types []string, want [][]any) { run(referenceSelect(q, columns, types, want)) }
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
			referenceRunStep(t, ctx, referenceHeldStatement{referenceExecutor: c, statement: s, query: q}, backend.recorder, referenceStep{query: q, args: []any{int64(10)}, affected: 1})
			rows("SELECT id FROM "+bSchema+".held", []string{"id"}, []string{"INT4"}, nil)
			exec("INSERT INTO "+bSchema+".held VALUES(7)", 1)
			exec("CREATE SEQUENCE "+aSchema+".x START 100 CACHE 1", 0)
			exec("ALTER SEQUENCE "+bSchema+".y RENAME TO identity", 0)
			failed := func() {
				want := controlsReferenceDiagnostic("42P01", `relation "y" does not exist`)
				want.Position = strconv.Itoa(strings.Index(q, "'y'") + 1)
				referenceRunStep(t, ctx, referenceHeldStatement{referenceExecutor: c, statement: s, query: q}, backend.recorder, referenceStep{query: q, args: []any{int64(150)}, state: want.Code, diagnostic: &want, errorStage: "execute"})
				rows("SELECT id FROM "+bSchema+".held", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			failed()
			state(aSchema+".x", 100, false)
			state(bSchema+".x", 10, true)
			state(bSchema+".identity", 20, true)
			run(sequenceInt64Step(referenceSelect("SELECT currval('"+bSchema+".x')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(10)}})))
			run(sequenceInt64Step(referenceSelect("SELECT currval('"+bSchema+".identity')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(20)}})))
			run(sequenceInt64Step(referenceSelect("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{int64(20)}})))
			undefinedShadow := controlsReferenceDiagnostic("55000", `currval of sequence "x" is not yet defined in this session`)
			run(referenceStep{query: "SELECT currval('" + aSchema + ".x')", selectRows: true, state: undefinedShadow.Code, diagnostic: &undefinedShadow, errorStage: "execute"})
			exec("CREATE SEQUENCE "+aSchema+".y START 200 CACHE 1", 0)
			deleted := func(n int64) {
				referenceRunStep(t, ctx, referenceHeldStatement{referenceExecutor: c, statement: s, query: q}, backend.recorder, referenceStep{query: q, args: []any{n}, affected: 1})
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
			run(sequenceInt64Step(referenceSelect("SELECT currval('"+aSchema+".x')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(150)}})))
			run(sequenceInt64Step(referenceSelect("SELECT currval('"+aSchema+".y')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(200)}})))
			run(sequenceInt64Step(referenceSelect("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{int64(200)}})))
			exec("SET search_path TO "+aSchema+","+bSchema, 0)
			deleted(151)
			state(aSchema+".x", 151, true)
			state(aSchema+".y", 201, true)
		})
	}
}
