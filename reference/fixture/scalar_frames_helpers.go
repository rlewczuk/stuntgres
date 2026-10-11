package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"sync"
	"testing"
)

// A recorder belongs to each execution context, so a concurrent same-SQL
// execution can never satisfy another frame's expected middleware rows.
func ScalarConcurrentFrames(t *testing.T, p *Pair, backend Backend, held *sql.Stmt, q string, count int, names, types []string, frame func(int) ([]any, [][]any)) {
	t.Helper()
	start := make(chan struct{})
	var joined sync.WaitGroup
	for n := range count {
		joined.Go(func() {
			<-start
			args, want := frame(n)
			recorder := &Recorder{RawBackend: backend.Recorder.RawBackend}
			ctx := context.WithValue(p.Ctx, RecorderKey{}, recorder)
			rows, err := held.QueryContext(ctx, args...)
			if err != nil {
				t.Error(n, err)
				return
			}
			ScalarRows(t, backend.PG, rows, names, types, want, false)
			recorder.Mu.Lock()
			defer recorder.Mu.Unlock()
			found := false
			for _, operation := range recorder.Operations {
				if operation.SQL != q || operation.Columns == nil || operation.Err != nil {
					continue
				}
				found = true
				if !reflect.DeepEqual(operation.Columns, names) || !operation.Closed || operation.CloseErr != nil {
					t.Error(n, "concurrent recorded columns/closure", operation)
				}
				normalized := [][]any{}
				for _, row := range operation.Rows {
					values := make([]any, len(row))
					for i, v := range row {
						AssertRawValue(t, backend.PG, types[i], v)
						values[i] = Normalize(v)
					}
					normalized = append(normalized, values)
				}
				if _, ok := CompareRows(normalized, want, false); !ok {
					t.Error(n, "concurrent recorded rows", normalized, want)
				}
			}
			if !found {
				t.Error(n, "missing execution-owned middleware result")
			}
		})
	}
	close(start)
	joined.Wait()
}
