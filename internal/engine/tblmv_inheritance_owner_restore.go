package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

const restoreInheritedOwnerBoundary = "restore accepts only immediately paired inherited declaration ownership"

type restoredInheritedOwner struct {
	ref     ids.ObjectRef
	owner   authorization.PrincipalRef
	creator authorization.PrincipalRef
}
