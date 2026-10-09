package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedNamespaceCallback(t *testing.T) {
	for _, event := range []string{"Create", "Rename", "ACL"} {
		t.Run(event, func(t *testing.T) {
			p := newReferenceScenario(t)
			backends := p.backends()
			for _, backend := range backends {
				if event == "ACL" && !backend.pg {
					continue
				} // O: actual GRANT event.
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string) { t.Helper(); sequenceProbeExec(t, p.ctx, c, q) }
					if backend.name == "postgres" {
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
					held, err := c.PrepareContext(p.ctx, "SELECT nextval('s')")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					value := func(want int64) {
						t.Helper()
						referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: held, query: "SELECT nextval('s')"}, backend.recorder, sequenceInt64Step(referenceStep{query: "SELECT nextval('s')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{want}}}))
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
					sequenceProbeValue(t, p.ctx, c, "SELECT last_value FROM b.s", 11)
					sequenceProbeValue(t, p.ctx, c, "SELECT last_value FROM a.s", 100)
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 101)
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('b.s')", 12)
				})
			}
		})
	}
}
