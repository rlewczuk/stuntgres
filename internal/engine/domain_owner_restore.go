package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

const restoreDomainOwnerBoundary = "restore accepts only immediately paired fresh domain ownership"

type restoredDomainOwner struct {
	ref            ids.ObjectRef
	domain         *catalog.Type
	namespace      *catalog.Namespace
	owner, creator authorization.PrincipalRef
}
