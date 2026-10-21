package table

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

const RestoreInheritedOwnerBoundary = "restore accepts only immediately paired inherited declaration ownership"

type RestoredInheritedOwner struct {
	Ref     ids.ObjectRef
	Owner   authorization.PrincipalRef
	Creator authorization.PrincipalRef
}
