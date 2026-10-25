package catalog

import (
	"stuntgres/internal/engine/ids"
)

// DependencyPurpose distinguishes written definition references from captures
// needed only to execute an aggregate's selected implementation.
type DependencyPurpose uint8

const (
	DefinitionDependency DependencyPurpose = iota
	AggregateSupportDependency
)

func (p DependencyPurpose) Valid() bool {
	return p == DefinitionDependency || p == AggregateSupportDependency
}

type ExpressionDependency struct {
	Ref      ids.ObjectRef
	Revision ids.DefinitionRevision
	Purpose  DependencyPurpose
}
