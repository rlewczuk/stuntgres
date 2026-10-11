package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedNamespaceCallback(t *testing.T) {
	for _, event := range []string{"Create", "Rename", "ACL"} {
		t.Run(event, func(t *testing.T) {
			p := fixture.NewScenario(t)
			backends := p.Backends()
			for _, backend := range backends {
				if event == "ACL" && !backend.PG {
					continue
				} // O: actual GRANT event.
				t.Run(backend.Name, func(t *testing.T) {
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string) { t.Helper(); fixture.SequenceProbeExec(t, p.Ctx, c, q) }
					if backend.Name == "postgres" {
						defer func() {
							exec("SET search_path TO public")
							for _, name := range []string{"a", "b", "unrelated", "callback_created", "callback_renamed"} {
								exec("DROP SCHEMA IF EXISTS " + name + " CASCADE")
							}
						}()
					}
					for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SCHEMA unrelated", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
						exec(q)
					}
					held, err := c.PrepareContext(p.Ctx, "SELECT nextval('s')")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					value := func(want int64) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: "SELECT nextval('s')"}, backend.Recorder, fixture.SequenceInt64Step(fixture.Step{Query: "SELECT nextval('s')", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{want}}}))
					}
					value(10)
					exec("CREATE SEQUENCE a.s START 100 CACHE 1")
					value(11)
					switch event {
					case "Create":
						exec("CREATE SCHEMA callback_created")
					case "Rename":
						exec("ALTER SCHEMA unrelated RENAME TO callback_renamed")
					case "ACL":
						exec("GRANT USAGE ON SCHEMA unrelated TO PUBLIC")
					}
					value(100)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT last_value FROM b.s", 11)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT last_value FROM a.s", 100)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('s')", 101)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('b.s')", 12)
				})
			}
		})
	}
}
