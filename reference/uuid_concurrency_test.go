package reference_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestReferenceUUIDHeldConcurrency(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE repeats(a UUID,b UUID)", 0))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO repeats VALUES($1,$1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, arg := range []any{uuidA, nil} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, arg))
			}
			start := make(chan struct{})
			errors := make(chan error, 8)
			var workers sync.WaitGroup
			for range 8 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					result, err := stmt.ExecContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), uuidF)
					if err == nil {
						var count int64
						count, err = result.RowsAffected()
						if err == nil && count != 1 {
							err = fmt.Errorf("concurrent count %d", count)
						}
					}
					errors <- err
				}()
			}
			close(start)
			workers.Wait()
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatal(err)
				}
			}
			want := [][]any{{uuidA, uuidA}}
			for range 8 {
				want = append(want, []any{uuidF, uuidF})
			}
			want = append(want, []any{nil, nil})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b"}, []string{"UUID", "UUID"}, want))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT a,b FROM repeats WHERE a=$1", []string{"a", "b"}, []string{"UUID", "UUID"}, [][]any{{uuidA, uuidA}}, uuidA))
		}()
	}
}
