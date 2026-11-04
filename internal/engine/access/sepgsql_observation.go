package access

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
)

// SepgsqlObservation is historical actual-phase evidence, never an allow cache.
// Expected records are committed predecessors; Actual records may belong to the
// command's own private/candidate lineage. Neither is compared to final next.
type SepgsqlObservation struct {
	Reference                    ids.ObjectRef
	ActualObject, ExpectedObject catalog.Descriptor
	Key                          catalog.SecurityLabelKey
	ActualLabel, ExpectedLabel   *catalog.SecurityLabelRecord
	Policy                       *sepgsql.Runtime
	Subject, Target, Class       string
	Mask                         sepgsql.Mask
	Generation, Frame            uint64
	Depth                        int
}

// SepgsqlObservationMark separates deduplicated observations from completed
// receipt age. Completion watermarks are monotonic and are never rewound.
type SepgsqlObservationMark struct {
	Observations    int
	Initializations uint64
	Retirements     uint64
}
type SepgsqlObservationSink interface {
	ObserveSepgsql(SepgsqlObservation) error
	MarkSepgsqlObservations() SepgsqlObservationMark
	RestoreSepgsqlObservations(SepgsqlObservationMark)
}

func (v View) observeSepgsql(address ids.ObjectAddress, class string, mask sepgsql.Mask) error {
	if v.Sepgsql.Observer == nil {
		return nil
	}
	parent := address
	parent.SubID = 0
	key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
	predecessor := v.Sepgsql.Predecessor
	if predecessor == nil {
		return ids.ErrCatalogReplaced
	}
	return v.Sepgsql.Observer.ObserveSepgsql(SepgsqlObservation{
		Reference: v.Ref(parent), ActualObject: v.Catalog.Get(parent), ExpectedObject: predecessor.Get(parent), Key: key,
		ActualLabel: v.Catalog.State().SecurityLabel(key), ExpectedLabel: predecessor.SecurityLabel(key),
		Policy: v.Sepgsql.Policy, Subject: v.Sepgsql.Subject, Target: v.SepgsqlContext(address), Class: class, Mask: mask,
		Generation: v.Sepgsql.Generation, Frame: v.Sepgsql.Frame, Depth: v.Sepgsql.Depth,
	})
}

// SepgsqlLabelBasis observes transition/default selection without inventing an
// EXECUTE permission for direct native dictionary callbacks.
func (v View) SepgsqlLabelBasis(address ids.ObjectAddress) error {
	if v.Sepgsql.Policy == nil {
		return nil
	}
	parent := address
	parent.SubID = 0
	descriptor, err := v.Object(v.Ref(parent))
	if err != nil {
		return err
	}
	return v.observeSepgsql(address, catalog.SepgsqlClass(descriptor, address.SubID), 0)
}
