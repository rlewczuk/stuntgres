package catalogrt

import (
	"context"

	"stuntgres/internal/engine/catalog"
)

// Exported names of catalogrt internals for the external tests in this
// directory.

// CatalogViewLeafEvents calls catalogViewLeafEvents.
func CatalogViewLeafEvents(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32) (uint32, error) {
	return (Runtime{}).catalogViewLeafEvents(ctx, read, relation, subset)
}
