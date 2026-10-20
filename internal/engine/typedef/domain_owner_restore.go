package typedef

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

const RestoreDomainOwnerBoundary = "restore accepts only immediately paired fresh domain ownership"

type RestoredDomainOwner struct {
	Ref            ids.ObjectRef
	Domain         *catalog.Type
	Namespace      *catalog.Namespace
	Owner, Creator authorization.PrincipalRef
}
