package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// External providers are captured before any wait. Self references instead bind
// against the completed private candidate, which may own a newly added key.
type AlterForeignKeyAdmission struct {
	Target           ids.ObjectRef
	Storage          *storage.Relation
	Index            ids.ObjectAddress
	Constraint       ids.OID // Nonzero only for an existing selected validation fact.
	Parent, Provider *catalog.Relation
}

// ForeignKeyCreation is the statement's CREATE/ALTER TABLE foreign-key
// capture: the resolved REFERENCES targets of CREATE TABLE (KeyTargets, one
// per declaration, zero for a self reference), the admitted ADD FOREIGN KEY
// captures (AlterKeys, in ADD action order), and existing invalid selected keys
// (ValidationKeys, carrying exact parent/provider originals). These lists have
// distinct authority and lock modes; validation never consumes an ADD slot.
type ForeignKeyCreation struct {
	KeyTargets     []ids.ObjectRef
	AlterKeys      []AlterForeignKeyAdmission
	ValidationKeys []AlterForeignKeyAdmission
}
