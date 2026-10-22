package table

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// External providers are captured before any wait. Self references instead bind
// against the completed private candidate, which may own a newly added key.
type AlterForeignKeyAdmission struct {
	Target  ids.ObjectRef
	Storage *storage.Relation
	Index   ids.ObjectAddress
}

// ForeignKeyCreation is the statement's CREATE/ALTER TABLE foreign-key
// capture: the resolved REFERENCES targets of CREATE TABLE (KeyTargets, one
// per declaration, zero for a self reference) and the admitted ADD FOREIGN
// KEY captures of ALTER TABLE (AlterKeys, in action order).
type ForeignKeyCreation struct {
	KeyTargets []ids.ObjectRef
	AlterKeys  []AlterForeignKeyAdmission
}
