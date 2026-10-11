package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderViewUnaryPlusRegression(t *testing.T) {
	for _, key := range []string{"+1.5", "+1e0", "+2147483648"} {
		t.Run(key, func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2),(2),(3)", 4))
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
					run(fixture.Exec("CREATE VIEW constant_order AS SELECT id FROM t ORDER BY "+key, 0))
					run(fixture.Exec("CREATE VIEW later_order AS SELECT id FROM t ORDER BY "+key+",id DESC", 0))
					run(fixture.Exec("CREATE TABLE empty_t(id INT)", 0))
					run(fixture.Exec("CREATE VIEW empty_order AS SELECT id FROM empty_t ORDER BY "+key, 0))
					for _, name := range []string{"constant_order", "later_order", "empty_order"} {
						var definition string
						if err := backend.DB.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
							t.Fatal(err)
						}
						run(fixture.Exec("CREATE VIEW replay_"+name+" AS "+definition, 0))
					}
					executions := []fixture.Backend{backend}
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						restored := fixture.NewFixture(t, false, true)
						if err := stuntgres.Restore(restored.Ctx, restored.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						executions = append(executions, restored.Backends()[0])
					}
					for _, execution := range executions {
						run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, execution.DB, execution.Recorder, step) }
						for _, prefix := range []string{"", "replay_"} {
							step := fixture.Select("SELECT id FROM "+prefix+"constant_order", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}})
							step.Bag = true
							run(step)
							run(fixture.Prepared(step))
							ordered := fixture.Select("SELECT id FROM "+prefix+"later_order ORDER BY id DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {int64(1)}})
							run(ordered)
							run(fixture.Prepared(ordered))
							empty := fixture.Select("SELECT id FROM "+prefix+"empty_order", []string{"id"}, []string{"INT4"}, nil)
							run(empty)
							run(fixture.Prepared(empty))
						}
					}
				})
			}
		})
	}
}
