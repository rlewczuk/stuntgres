package catalog

import (
	"stuntgres/internal/engine/ids"
)

type ExpressionDependency struct {
	Ref      ids.ObjectRef
	Revision ids.DefinitionRevision
}
