package engine

import (
	"stuntgres/internal/engine/catalog"
)

type extensionAuthorityObservation struct {
	roles               uint64
	namespace, database catalog.Descriptor
}
