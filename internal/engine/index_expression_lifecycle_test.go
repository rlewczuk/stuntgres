package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/storage"
)

// Inspect every tuple component, not merely the leading ordering key.
func expressionRootTuples(t *testing.T, root *storage.IndexRoot) [][]string {
	t.Helper()
	btreeKeys(t, root)
	tuples := make([][]string, len(root.Entries))
	for i, entry := range root.Entries {
		tuples[i] = make([]string, len(entry.Keys))
		for j, key := range entry.Keys {
			if key.IsNull() {
				tuples[i][j] = "NULL"
				continue
			}
			var err error
			tuples[i][j], err = key.Type().Output(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return tuples
}
