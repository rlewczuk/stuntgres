package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func batchSessionEffects(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchExec(t, p, b, a, "SET application_name='entry'", 0)
		for _, change := range []string{"SET application_name='new'", "RESET application_name", "RESET ALL", "SELECT set_config('application_name','new',false)"} {
			q := change + "; SELECT missing"
			w := batchDiag("42703", `column "missing" does not exist`)
			w.Position = batchPosition(q, "missing")
			batchError(t, p, a, q, w)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"entry"}}))
		}
		batchExec(t, p, b, a, "PREPARE removed AS SELECT 1", 0)
		q := "DEALLOCATE removed; SELECT missing"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		batchError(t, p, a, q, w)
		batchError(t, p, a, "EXECUTE removed", batchDiag("26000", `prepared statement "removed" does not exist`))
		// Compare sequence relationships within each backend, never allocator/runtime goldens.
		sample := func() float64 {
			var v float64
			if err := a.QueryRowContext(p.Ctx, "SELECT random()").Scan(&v); err != nil {
				t.Fatal(err)
			}
			if v < 0 || v >= 1 {
				t.Fatal("random range", v)
			}
			return v
		}
		if _, err := a.ExecContext(p.Ctx, "SELECT setseed(0.25)"); err != nil {
			t.Fatal(err)
		}
		first := sample()
		second := sample()
		third := sample()
		if _, err := a.ExecContext(p.Ctx, "SELECT setseed(0.25)"); err != nil {
			t.Fatal(err)
		}
		if got := sample(); got != first {
			t.Fatal("seed replay", got, first)
		}
		q = "SELECT random() AS v; SELECT missing"
		w.Position = batchPosition(q, "missing")
		r := batchQuery(t, p, a, q)
		if !r.Next() {
			t.Fatal(r.Err())
		}
		var consumed float64
		if err := r.Scan(&consumed); err != nil {
			t.Fatal(err)
		}
		if consumed != second {
			t.Fatal("generator sequence", consumed, second)
		}
		r.Next()
		fixture.AssertDiagnostic(t, r.Err(), &w)
		r.Close()
		if got := sample(); got != third {
			t.Fatal("rollback rewound random state", got, third)
		}
	})
}
