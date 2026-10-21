package view

import (
	"stuntgres/internal/engine/visibility"
)

// MaterializedStage is a catalog stage's materialized-view refresh state:
// the replacement being published and the source snapshot it read. Only
// REFRESH and materialized population write it.
type MaterializedStage struct {
	Replacement *MaterializedReplacement
	SourceView  *visibility.Data
}
