package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type PhysicalNotNullCauseKind uint8

const (
	PhysicalNotNullTypeCleanup PhysicalNotNullCauseKind = iota + 1
	PhysicalNotNullImportCleanup
	PhysicalNotNullImportFlag
)

// A missing NN row is not a generic alternative to an NN constraint. This
// immutable cause is issued only by real TYPE cleanup or its exact logical
// import counterpart. Archived identities are not current provider edges.
type PhysicalNotNullCause struct {
	Self      *PhysicalNotNullCause
	Kind      PhysicalNotNullCauseKind
	Ref       ids.ObjectRef
	Origin    ids.ObjectRef // Immutable archive namespace; never resolved as a provider.
	Attribute int32
	RemovedNN ids.OID
	Primary   ids.OID
	Index     ids.ObjectAddress
	Usage     sqltype.Usage
}

type PhysicalPrimaryKeyUsage struct {
	Attribute int32
	Usage     sqltype.Usage
}

type PhysicalPrimarySupport struct {
	Constraint ids.OID
	Index      ids.ObjectAddress
	Keys       []PhysicalPrimaryKeyUsage
}

type PhysicalNotNullProof struct {
	Cause   *PhysicalNotNullCause
	Usage   sqltype.Usage
	Support *PhysicalPrimarySupport
	Retired *PhysicalPrimarySupport // Genuine later key retirement, not a live edge.
}
