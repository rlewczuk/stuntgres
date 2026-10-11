package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceTypeCoreHeldConcurrency(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE repeats(a INT,b TEXT,c INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE typed(i INT,b BIGINT,t TEXT,f BOOL)", 0))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			q := "INSERT INTO repeats VALUES($1,$2,$1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, n := range []int{7, 8} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, n, "x"))
			}
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			start := make(chan struct{})
			failures := make(chan error, 16)
			var wg sync.WaitGroup
			for n := range 16 {
				wg.Go(func() {
					<-start
					result, err := stmt.ExecContext(ctx, n+20, "parallel")
					if err == nil {
						var count int64
						count, err = result.RowsAffected()
						if err == nil && count != 1 {
							t.Errorf("affected=%d", count)
						}
					}
					failures <- err
				})
			}
			close(start)
			wg.Wait()
			close(failures)
			for err := range failures {
				if err != nil {
					t.Fatal(err)
				}
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a,c FROM repeats WHERE b='x' ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}, {int64(8), int64(8)}}))
			want := make([][]any, 16)
			for n := range 16 {
				want[n] = []any{int64(n + 20), int64(n + 20)}
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a,c FROM repeats WHERE b='parallel' ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT4"}, want))
		})
	}
	// Existing unsupported mixed inference is a native capability boundary,
	// not a claim that PostgreSQL rejects the same SQL.
	for _, q := range []string{"INSERT INTO repeats VALUES($1,$1,1)", "INSERT INTO typed VALUES($1,$1,NULL,NULL)"} {
		stmt, err := p.Memory.PrepareContext(p.Ctx, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed Prepare exposed a statement")
		}
		fixture.State(t, err, "0A000")
	}
}
