package engine

import "stuntgres/internal/engine/catalog"

// tableStage is a catalog stage's table work. Only CREATE TABLE ... LIKE
// writes it: the exact source descriptors it copied, which publication
// re-checks but which are not dependencies.
type tableStage struct {
	likeSources []catalog.Descriptor
}
