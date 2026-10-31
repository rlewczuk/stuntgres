package core_test

import (
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

func clusterStorageIDs(store *storage.Relation) []int64 {
	result := make([]int64, len(store.Versions))
	for i, row := range store.Versions {
		result[i] = enginetest.Export(row.Values[0]).(int64)
	}
	return result
}
