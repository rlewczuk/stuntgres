package engine

import (
	"stuntgres/internal/engine/ids"
)

type domainCheckIdentity struct {
	transaction ids.TxRef
	command     ids.CommandID
}
