package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceUUIDHeldConcurrency(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE repeats(a UUID,b UUID)", 0))
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO repeats VALUES($1,$1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, arg := range []any{uuidA, nil} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, arg))
			}
			start := make(chan struct{})
			errors := make(chan error, 8)
			var workers sync.WaitGroup
			for range 8 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					result, err := stmt.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), uuidF)
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
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT * FROM repeats ORDER BY a", []string{"a", "b"}, []string{"UUID", "UUID"}, want))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a,b FROM repeats WHERE a=$1", []string{"a", "b"}, []string{"UUID", "UUID"}, [][]any{{uuidA, uuidA}}, uuidA))
		}()
	}
}
