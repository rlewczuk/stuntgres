package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// External providers are captured before any wait. Self references instead bind
// against the completed private candidate, which may own a newly added key.
type alterForeignKeyAdmission struct {
	Target  ids.ObjectRef
	Storage *storage.Relation
	Index   ids.ObjectAddress
}
