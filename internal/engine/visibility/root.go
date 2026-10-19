package visibility

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
)

// Root is one relation's retained repeatable-read root: the immutable
// committed versions and end markers captured at snapshot time, with the
// storage incarnation and descriptor they belong to. It never copies rows.
type Root struct {
	Versions []storage.RowVersion
	Ends     *storage.DeletionRoot
	Storage  *storage.Relation
	Relation *catalog.Relation
}
