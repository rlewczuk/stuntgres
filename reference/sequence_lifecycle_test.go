package reference_test

import (
	"database/sql"
	"sync"
	"testing"
)

func TestReferenceSequenceConcurrentBagAndLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE bag CACHE 1")
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
						if err := c.QueryRowContext(p.ctx, "SELECT nextval('bag')").Scan(&v); err != nil {
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
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceStep{query: "SELECT last_value FROM bag", selectRows: true, columns: []string{"last_value"}, types: []string{"INT8"}, rows: [][]any{{int64(64)}}})
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE life")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('life')", 1)
			for _, tc := range []struct {
				end  string
				want int64
			}{{"ROLLBACK AND CHAIN", 2}, {"COMMIT AND CHAIN", 100}} {
				sequenceProbeExec(t, p.ctx, a, "BEGIN")
				sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE life RESTART 100")
				sequenceProbeExec(t, p.ctx, a, tc.end)
				sequenceProbeValue(t, p.ctx, b, "SELECT nextval('life')", tc.want)
				sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			}
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE life RESTART 200")
			missing := controlsReferenceDiagnostic("42P01", `relation "absent" does not exist`)
			missing.Position = "16"
			sequenceProbeError(t, p.ctx, a, "SELECT nextval('absent')", &missing)
			sequenceProbeExec(t, p.ctx, a, "COMMIT")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('life')", 101)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE cached CACHE 10")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('cached')", 1)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('cached')", 11)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('cached')", 2)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE cached RESTART 100")
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('cached')", 12)
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE cached RESTART 100")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('cached')", 100)
			sequenceProbeExec(t, p.ctx, a, "CREATE TEMP SEQUENCE life START 7")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('life')", 7)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('life')", 102)
		})
	}
}
