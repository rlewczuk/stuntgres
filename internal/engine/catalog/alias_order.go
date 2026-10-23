package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// AliasOrderEndpoint is a captured numeric alias identity. Object is present
// only when the source actually contains that object; dangling numbers remain
// fixed. Domain separates fixed numbers from unrelated alias classes.
type AliasOrderEndpoint struct {
	Object ids.ObjectAddress
	OID    ids.OID
	Domain sqltype.ID
}

// AliasOrderConstraint preserves one actual comparison relationship. Equal
// endpoints stay equal; otherwise Lower's numeric identity must remain smaller.
// These are operation-local structural facts, never callable providers.
type AliasOrderConstraint struct {
	Lower, Upper AliasOrderEndpoint
}
