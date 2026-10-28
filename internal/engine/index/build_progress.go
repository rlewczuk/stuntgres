package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// BuildProgress observes completed real work. Implementations must not call back
// into the engine: builders may execute beneath publication locks.
type BuildProgress interface {
	ScanStart(versions int, method catalog.IndexMethod)
	ScanDone(visited int)
	ScanFinish(tuples int, method catalog.IndexMethod)
}

// ProgressBinding selects one index, never every rebuild of its parent.
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
	if b.Targets != nil {
		parent, ok := b.Targets[address]
		if !ok {
			return nil
		}
		if observer, ok := b.Observer.(interface {
			IndexStart(ids.ObjectAddress, ids.ObjectAddress)
		}); ok {
			observer.IndexStart(parent, address)
		}
	} else if b.Index != address {
		return nil
	}
	return b.Observer
}
