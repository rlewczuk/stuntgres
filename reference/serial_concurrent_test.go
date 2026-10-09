package reference_test

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

func TestReferenceSerialConcurrentAllocation(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					setup := referenceExec(`CREATE TABLE t(id SERIAL)`, 0)
					setup.nonContext = source
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, setup)
					const count = 12
					var workers sync.WaitGroup
					start := make(chan struct{})
					errors := make(chan error, count)
					for range count {
						workers.Add(1)
						go func() {
							defer workers.Done()
							<-start
							var r sql.Result
							var err error
							if source {
								r, err = backend.db.Exec(`INSERT INTO t DEFAULT VALUES`)
							} else {
								r, err = backend.db.ExecContext(p.ctx, `INSERT INTO t DEFAULT VALUES`)
							}
							if err == nil {
								n, e := r.RowsAffected()
								err = e
								if err == nil && n != 1 {
									err = fmt.Errorf("affected %d", n)
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
					want := make([][]any, count)
					for i := range count {
						want[i] = []any{int64(i + 1)}
					}
					selected := referenceSelect(`SELECT * FROM t ORDER BY id`, []string{"id"}, []string{"INT4"}, want)
					selected.nonContext = source
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, selected)
				})
			}
		})
	}
}
