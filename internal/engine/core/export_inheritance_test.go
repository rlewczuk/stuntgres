package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// ScopeAcquireInheritanceSources calls x.acquireInheritanceSources.
func ScopeAcquireInheritanceSources(x *StatementScope, grants map[ids.ObjectRef]lock.Mode, imported ...[]exec.Leaf) error {
	return x.acquireInheritanceSources(grants, imported...)
}

// ScopeInheritanceRead calls x.inheritanceRead.
func ScopeInheritanceRead(x *StatementScope, current catalog.Read) catalog.Read {
	return x.inheritanceRead(current)
}

// InheritanceScopeCatalog returns x.catalog.
func InheritanceScopeCatalog(x *inheritanceScope) catalog.Read { return x.catalog }

// InheritanceScopeRelations returns x.relations.
func InheritanceScopeRelations(x *inheritanceScope) []exec.Leaf { return x.relations }
