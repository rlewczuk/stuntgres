package engine

import (
	"stuntgres/internal/engine/visibility"
)

// materializedStage is a catalog stage's materialized-view refresh state:
// the replacement being published and the source snapshot it read. Only
// REFRESH and materialized population write it.
type materializedStage struct {
	Replacement *materializedReplacement
	SourceView  *visibility.Data
}
