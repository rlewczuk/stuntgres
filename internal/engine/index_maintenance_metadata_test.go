package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func indexMaintenanceMetadataFlags(idx, parent ids.OID, n int16) index.Metadata {
	return index.Metadata{IndexRelID: idx, IndRelID: parent, IndNatts: n, IndNKeyAtts: n, IndImmediate: true, IndIsValid: true, IndIsReady: true, IndIsLive: true}
}

func indexMaintenanceMetadata(t *testing.T, e *core.Engine, catalog catalog.Read, name string) (index.Metadata, index.IndexesMetadata) {
	t.Helper()
	ref := roleTableRef(t, e, name)
	m, n, err := index.ProjectIndexMetadata(context.Background(), catalog, ref)
	if err != nil {
		t.Fatal(err)
	}
	return m, n
}
