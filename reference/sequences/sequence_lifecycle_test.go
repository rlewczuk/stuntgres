package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceSequenceConcurrentBagAndLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE bag CACHE 1")
			const each = 32
			values := make(chan int64, 2*each)
			failures := make(chan error, 2)
			var wg sync.WaitGroup
			for _, c := range []*sql.Conn{a, b} {
				wg.Add(1)
				go func(c *sql.Conn) {
					defer wg.Done()
					for range each {
						var v int64
						if err := c.QueryRowContext(p.Ctx, "SELECT nextval('bag')").Scan(&v); err != nil {
							failures <- err
							return
						}
						values <- v
					}
				}(c)
			}
			wg.Wait()
			close(values)
			close(failures)
			for err := range failures {
				t.Fatal(err)
			}
			seen := make(map[int64]int)
			for v := range values {
				seen[v]++
			}
			if len(seen) != 2*each {
				t.Fatal("wrong bag", seen)
			}
			for v := int64(1); v <= 2*each; v++ {
				if seen[v] != 1 {
					t.Fatal("bag multiplicity", v, seen[v])
				}
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Step{Query: "SELECT last_value FROM bag", SelectRows: true, Columns: []string{"last_value"}, Types: []string{"INT8"}, Rows: [][]any{{int64(64)}}})
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE life")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('life')", 1)
			for _, tc := range []struct {
				end  string
				want int64
			}{{"ROLLBACK AND CHAIN", 2}, {"COMMIT AND CHAIN", 100}} {
				fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
				fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE life RESTART 100")
				fixture.SequenceProbeExec(t, p.Ctx, a, tc.end)
				fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('life')", tc.want)
				fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			}
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE life RESTART 200")
			missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "absent" does not exist`)
			missing.Position = "16"
			fixture.SequenceProbeError(t, p.Ctx, a, "SELECT nextval('absent')", &missing)
			fixture.SequenceProbeExec(t, p.Ctx, a, "COMMIT")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('life')", 101)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE cached CACHE 10")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('cached')", 1)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('cached')", 11)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('cached')", 2)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE cached RESTART 100")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('cached')", 12)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE cached RESTART 100")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('cached')", 100)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TEMP SEQUENCE life START 7")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('life')", 7)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('life')", 102)
		})
	}
}
