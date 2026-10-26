package catalog

import (
	"stuntgres/internal/engine/ids"
)

// DependencyPurpose separates written executable references, selected aggregate
// support captures, and identities used only as constant-input operands.
type DependencyPurpose uint8

const (
	DefinitionDependency DependencyPurpose = iota
	AggregateSupportDependency
	OperandDependency
)

func (p DependencyPurpose) Valid() bool {
	return p == DefinitionDependency || p == AggregateSupportDependency || p == OperandDependency
}

type ExpressionDependency struct {
	Ref      ids.ObjectRef
	Revision ids.DefinitionRevision
	Purpose  DependencyPurpose
}
