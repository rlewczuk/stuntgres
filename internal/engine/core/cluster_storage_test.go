package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func clusterStorageSeed(t *testing.T) (*core.Engine, *session.Session, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id int,k int,payload text)", "INSERT INTO t VALUES(1,2,'b'),(2,1,'a'),(3,2,'b'),(4,NULL,NULL),(5,3,'c')", "CREATE INDEX a ON t(k)", "CREATE INDEX b ON t((lower(payload)) DESC NULLS FIRST,k) INCLUDE(id)", "CREATE UNIQUE INDEX u ON t(id)", "CREATE TABLE unrelated(id int)", "INSERT INTO unrelated VALUES(9)", "CREATE INDEX unrelated_idx ON unrelated(id)"} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s, enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address
}

func clusterStorageIDs(store *storage.Relation) []int64 {
	result := make([]int64, len(store.Versions))
	for i, row := range store.Versions {
		result[i] = enginetest.Export(row.Values[0]).(int64)
	}
	return result
}
