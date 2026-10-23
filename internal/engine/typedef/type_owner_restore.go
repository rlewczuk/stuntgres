package typedef

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func RestoreTypeOwnerBoundary(kind string) string {
	if kind == "domain" {
		return "restore accepts only immediately paired fresh domain ownership"
	}
	return "restore accepts only immediately paired fresh range ownership"
}

type RestoredTypeOwner struct {
	Ref            ids.ObjectRef
	Type           *catalog.Type
	Kind           string
	Namespace      *catalog.Namespace
	Owner, Creator authorization.PrincipalRef
}
