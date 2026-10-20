package typedef

import (
	"stuntgres/internal/engine/ids"
)

type DomainCheckIdentity struct {
	Transaction ids.TxRef
	Command     ids.CommandID
}
