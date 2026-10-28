package visibility

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	// Indexes are supporting unique roots captured at the same stable frontier.
	// They detect snapshot/current RI disagreement, never current integrity.
	Indexes map[ids.ObjectAddress]IndexSnapshot
}

type IndexSnapshot struct {
	Definition *catalog.Relation
	Root       *storage.IndexRoot
}
