package table

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// ExplicitLockMode is the lock a LOCK TABLE mode takes; 0 for an unknown mode.
func ExplicitLockMode(mode parser.TableLockMode) lock.Mode {
	switch mode {
	case parser.LockAccessShare:
		return lock.AccessShare
	case parser.LockRowShare:
		return lock.RowShare
	case parser.LockRowExclusive:
		return lock.RowExclusive
	case parser.LockShareUpdateExclusive:
		return lock.ShareUpdateExclusive
	case parser.LockShare:
		return lock.Share
	case parser.LockShareRowExclusive:
		return lock.ShareRowExclusive
	case parser.LockExclusive:
		return lock.Exclusive
	case parser.LockAccessExclusive:
		return lock.AccessExclusive
	}
	return 0
}

// CheckExplicitLock checks that LOCK TABLE may take mode on d, a relation of
// v's catalog: the relation kind first, then v's rights on ref, read through
// env. Any of the privileges PostgreSQL accepts for the mode suffices.
func CheckExplicitLock(v access.View, env access.Environment, ref ids.ObjectRef, d *catalog.Relation, mode lock.Mode) error {
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.ViewRelation {
		return analyze.TableKindError(d, "lock")
	}
	rights, err := v.Effective(env, ref, false)
	if err != nil {
		return err
	}
	alternatives := authorization.MaintainPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege
	if mode == lock.AccessShare || mode == lock.RowShare || mode == lock.RowExclusive {
		alternatives |= authorization.InsertPrivilege
	}
	if mode == lock.AccessShare {
		alternatives |= authorization.SelectPrivilege
	}
	if rights&alternatives != 0 {
		if catalog.HierarchyRelation(d) {
			return sqlError("0A000", "locking partition hierarchies is unsupported")
		}
		return nil
	}
	kind := "table"
	if d.RelationKind == catalog.ViewRelation {
		kind = "view"
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, d.Name()))
}
