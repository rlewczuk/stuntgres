package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/statistics"
)

// BuildProgress observes completed real work. Implementations must not call back
// into the engine: builders may execute beneath publication locks.
type BuildProgress interface {
	ScanStart(versions int, method catalog.IndexMethod)
	ScanDone(visited int)
	ScanFinish(tuples int, method catalog.IndexMethod)
}

// ProgressBinding selects admitted index targets, never every parent rebuild.
type ProgressBinding struct {
	Index      ids.ObjectAddress
	Targets    map[ids.ObjectAddress]ids.ObjectAddress
	Observer   BuildProgress
	partitions *partitionBuildProgress
}

func (b ProgressBinding) Capture(address ids.ObjectAddress) *BuildCapture {
	observer := b.ObserverFor(address)
	if observer == nil {
		return nil
	}
	return &BuildCapture{Progress: observer}
}

func (b ProgressBinding) ObserverFor(address ids.ObjectAddress) BuildProgress {
	if b.Observer == nil {
		return nil
	}
	parent, selected := b.target(address)
	if !selected {
		return nil
	}
	if b.Targets != nil {
		if observer, ok := b.Observer.(interface {
			IndexStart(ids.ObjectAddress, ids.ObjectAddress)
		}); ok {
			observer.IndexStart(parent, address)
		}
	}
	return b.Observer
}

func (b ProgressBinding) target(address ids.ObjectAddress) (ids.ObjectAddress, bool) {
	if b.Targets != nil {
		parent, ok := b.Targets[address]
		return parent, ok
	}
	return ids.ObjectAddress{}, b.Index == address
}

// Only the selected admitted SQL utility build carries Progress. Validation,
// reconstruction and native Restore do not acquire heap-read authority merely
// because they use the same key builder. Capture once at the actual scan start.
func (b *BuildCapture) beginHeapScan(evaluation *exec.Context, parent *catalog.Relation) statistics.RelationAccess {
	if b == nil || b.Progress == nil {
		return statistics.RelationAccess{}
	}
	host := evaluation.LiveHost()
	if host == nil {
		return statistics.RelationAccess{}
	}
	ref := evaluation.Catalog.Owner().Domain.Ref(evaluation.Catalog.Epoch(), parent.Address())
	access := host.CaptureRelationAccess(ref)
	access.Record(1, 0, 0)
	return access
}
