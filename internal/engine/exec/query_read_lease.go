package exec

import "sort"

// SourceReadLease observes one actually reached storage invocation. Admission
// captures it without opening it. Each Open is a fresh actual invocation;
// the capture's transient session pin remains idempotent until owner Close.
// Implementations may enter engine authority only from Open, never Close.
type SourceReadLease interface {
	Open()
	Close()
}

func (s *Source) beginRead() {
	if s != nil && s.ReadLease != nil && s.Count() != 0 {
		s.ReadLease.Open()
	}
}

// Ordinary scans activate on opening; a sampler activates only when it consumes
// its initialized child. Rewind preserves this once-per-opened-stage frontier.
func (s *Stage) beginRead() {
	if s.readSource && s.Source != nil && s.Source.Inheritance == nil {
		s.Source.beginRead()
		s.readSource = false
	}
}
func (s *Stage) readSegment(index int) {
	segment := s.Source.Inheritance[index].Prefix
	if segment.ReadLease == nil || segment.Count() == 0 {
		return
	}
	if s.readSegments == nil {
		s.readSegments = make([]bool, len(s.Source.Inheritance))
	}
	if !s.readSegments[index] {
		segment.beginRead()
		s.readSegments[index] = true
	}
}
func (s *Stage) readOrdinal(index int) {
	if !s.readSource || s.Source == nil || s.Source.Inheritance == nil || index < 0 || index >= s.Source.Count() {
		return
	}
	s.readSegment(sort.SearchInts(s.Source.InheritanceEnds, index+1))
}
func (s *Source) finishReadLeases() {
	if s.ReadLease != nil {
		s.ReadLease.Close()
	}
	for _, index := range s.HashIndexes {
		if index.ReadLease != nil {
			index.ReadLease.Close()
		}
	}
	for _, index := range s.BloomIndexes {
		if index.ReadLease != nil {
			index.ReadLease.Close()
		}
	}
}
func (s *Stage) drainReadLeases() {
	if !s.readSource || s.Source == nil || s.Source.Inheritance == nil {
		return
	}
	if s.Selection == nil && !s.scanRange {
		for i := range s.Source.Inheritance {
			s.readSegment(i)
		}
	} else {
		for i := range s.Count() {
			s.readOrdinal(s.Index(i))
		}
	}
}
