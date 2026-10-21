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
	PhysicalNotNullDetachedTypeCleanup
	PhysicalNotNullInheritedTypeCleanup
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
	History   []RetiredInheritanceOrigin // Immutable detached TYPE source certificate.
	Source    *PhysicalNotNullCause      // Archived parent cleanup, not a live edge.
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

// Standalone exceptional flags need an issued cause, never an absent NN row
// alone. The immutable TYPE certificate is retained through current remapping.
func PhysicalNotNullStandaloneCause(cause *PhysicalNotNullCause) bool {
	if cause == nil || cause.Self != cause {
		return false
	}
	switch cause.Kind {
	case PhysicalNotNullImportFlag:
		return cause.Primary == 0 && cause.Index == (ids.ObjectAddress{})
	case PhysicalNotNullDetachedTypeCleanup:
		if len(cause.History) == 0 || len(cause.History) > 32767 || cause.Primary != 0 || cause.Index != (ids.ObjectAddress{}) {
			return false
		}
		for i, h := range cause.History {
			if !h.Detached || h.Own != cause.RemovedNN || h.Check || h.Constraint == 0 || h.Lifetime == 0 || h.Parent.Domain() == nil || h.Parent.Epoch == 0 || h.Parent.Address.ClassID != ids.RelationClassID || h.Parent.Address.ObjectID == 0 || h.Parent.Address.SubID != 0 || h.Attribute <= 0 || h.Attribute > 1600 || h.Column == "" || h.Name == "" || h.Replayed != (h.Archive != 0) {
				return false
			}
			for _, previous := range cause.History[:i] {
				if previous.Parent == h.Parent && previous.Archive == h.Archive && previous.Lifetime == h.Lifetime && previous.Constraint == h.Constraint {
					return false
				}
			}
		}
		return true
	case PhysicalNotNullInheritedTypeCleanup:
		// Sources are archival receipts, not resolvable dependencies. Iterative
		// validation rejects cycles without recursive stack growth.
		slow, fast := cause, cause
		for current := cause; current != nil; current = current.Source {
			if current.Self != current || current.RemovedNN == 0 || current.Attribute <= 0 {
				return false
			}
			if current.Kind == PhysicalNotNullDetachedTypeCleanup {
				return PhysicalNotNullStandaloneCause(current)
			}
			if current.Kind != PhysicalNotNullInheritedTypeCleanup || current.Primary != 0 || current.Index != (ids.ObjectAddress{}) {
				return false
			}
			slow = slow.Source
			if fast != nil {
				fast = fast.Source
			}
			if fast != nil {
				fast = fast.Source
			}
			if fast != nil && fast == slow {
				return false
			}
		}
	}
	return false
}
