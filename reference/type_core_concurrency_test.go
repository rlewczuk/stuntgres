package reference_test

import (
	"context"
	"sync"
	"testing"
)

func TestReferenceTypeCoreHeldConcurrency(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE repeats(a INT,b TEXT,c INT)", 0))
	p.step(t, referenceExec("CREATE TABLE typed(i INT,b BIGINT,t TEXT,f BOOL)", 0))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			q := "INSERT INTO repeats VALUES($1,$2,$1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, n := range []int{7, 8} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, n, "x"))
			}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
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
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT a,c FROM repeats WHERE b='x' ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}, {int64(8), int64(8)}}))
			want := make([][]any, 16)
			for n := range 16 {
				want[n] = []any{int64(n + 20), int64(n + 20)}
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT a,c FROM repeats WHERE b='parallel' ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT4"}, want))
		})
	}
	// Existing unsupported mixed inference is a native capability boundary,
	// not a claim that PostgreSQL rejects the same SQL.
	for _, q := range []string{"INSERT INTO repeats VALUES($1,$1,1)", "INSERT INTO typed VALUES($1,$1,NULL,NULL)"} {
		stmt, err := p.memory.PrepareContext(p.ctx, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed Prepare exposed a statement")
		}
		referenceState(t, err, "0A000")
	}
}
