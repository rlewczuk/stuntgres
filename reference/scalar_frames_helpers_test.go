package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"sync"
	"testing"
)

// A recorder belongs to each execution context, so a concurrent same-SQL
// execution can never satisfy another frame's expected middleware rows.
func scalarConcurrentFrames(t *testing.T, p *referencePair, backend referenceBackend, held *sql.Stmt, q string, count int, names, types []string, frame func(int) ([]any, [][]any)) {
	t.Helper()
	start := make(chan struct{})
	var joined sync.WaitGroup
	for n := range count {
		joined.Go(func() {
			<-start
			args, want := frame(n)
			recorder := &referenceRecorder{rawBackend: backend.recorder.rawBackend}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, recorder)
			rows, err := held.QueryContext(ctx, args...)
			if err != nil {
				t.Error(n, err)
				return
			}
			scalarRows(t, backend.pg, rows, names, types, want, false)
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			found := false
			for _, operation := range recorder.operations {
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
						referenceAssertRawValue(t, backend.pg, types[i], v)
						values[i] = referenceNormalize(v)
					}
					normalized = append(normalized, values)
				}
				if _, ok := referenceCompareRows(normalized, want, false); !ok {
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
