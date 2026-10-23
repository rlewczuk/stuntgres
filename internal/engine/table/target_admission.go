package table

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Target is how a statement admits the relation it targets, decided from its
// syntax alone, before any name is resolved or lock taken.
type Target struct {
	// Name is the target relation's name; Mode the lock taken on it.
	Name parser.ObjectName
	Mode lock.Mode
	// OwnerOnly requires owner rights on the target.
	OwnerOnly bool
	// Rename resolves Name as a utility target that may be missing: with
	// IfExists a missing relation admits nothing.
	Rename, IfExists bool
	// TableRename also requires CREATE on the target's namespace.
	TableRename bool
	// ViewReplacement targets the relation CREATE OR REPLACE VIEW replaces in
	// its creation namespace.
	ViewReplacement bool
}

// TargetEntry decides the target of the statement kinds its package owns. It
// returns false for other kinds, and for an owned statement that targets no
// relation in this mode (execute is false while preparing).
type TargetEntry func(stmt parser.Statement, execute bool) (Target, bool, error)

// AdmitTarget asks entries in order for stmt's target. The engine registers
// one entry per owning package; each statement kind belongs to one entry, so
// the order never changes the outcome. False means stmt targets no relation.
func AdmitTarget(entries []TargetEntry, stmt parser.Statement, execute bool) (Target, bool, error) {
	for _, entry := range entries {
		if target, ok, err := entry(stmt, execute); ok || err != nil {
			return target, ok, err
		}
	}
	return Target{}, false, nil
}

// TargetAdmission is the TargetEntry of the table statements: CREATE TABLE
// ... PARTITION OF, the ALTER TABLE family, COMMENT ON TABLE or COLUMN, and
// the relation targets of CREATE INDEX and ALTER ... OWNER TO, whose packages
// sit below table.
func TargetAdmission(stmt parser.Statement, execute bool) (Target, bool, error) {
	if !execute {
		return Target{}, false, nil
	}
	switch v := stmt.(type) {
	case *parser.CreateTrigger:
		return Target{Name: v.Table, Mode: lock.ShareRowExclusive}, true, nil
	case *parser.CreateTable:
		if v.PartitionOf == nil {
			return Target{}, false, nil
		}
		return Target{Name: v.PartitionOf.Name, Mode: lock.AccessExclusive, OwnerOnly: true}, true, nil
	case *parser.CreateIndex:
		target := Target{Name: v.Table, Mode: lock.Share, OwnerOnly: true}
		if v.Concurrent.Character != 0 {
			target.Mode = lock.ShareUpdateExclusive
		}
		return target, true, nil
	case *parser.AlterOwner:
		if v.Kind == "type" || v.Kind == "domain" || v.Kind == "database" {
			return Target{}, false, nil
		}
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.Comment:
		if v.Kind != parser.CommentTable && v.Kind != parser.CommentForeignTable && v.Kind != parser.CommentColumn {
			return Target{}, false, nil
		}
		name := v.Name
		if v.Kind == parser.CommentColumn {
			if len(name.Parts) < 2 {
				return Target{}, false, sqlError("42601", "column name must be qualified")
			}
			name.Parts = name.Parts[:len(name.Parts)-1]
		}
		return Target{Name: name, Mode: lock.ShareUpdateExclusive}, true, nil
	case *parser.AlterTable:
		return Target{Name: v.Name, Mode: AlterTableLockMode(v), OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.AlterTableAttachPartition:
		return Target{Name: v.Name, Mode: lock.ShareUpdateExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.AlterTableDetachPartition:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.AlterTableOf:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true}, true, nil
	case *parser.AlterTableDefault:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true}, true, nil
	case *parser.AlterTableRenameConstraint:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.AlterTableRenameColumn:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.AlterTableSetSchema:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.AlterTableRenameTable:
		return Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists, TableRename: true}, true, nil
	}
	return Target{}, false, nil
}
