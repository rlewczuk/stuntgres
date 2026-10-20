package extension

import (
	"stuntgres/internal/engine/catalog"
)

type AuthorityObservation struct {
	Roles               uint64
	Namespace, Database catalog.Descriptor
}
