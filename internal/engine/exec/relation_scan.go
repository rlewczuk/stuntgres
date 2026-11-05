package exec

import (
	"sort"
	"stuntgres/internal/engine/statistics"
)

// RelationScan belongs to one physical invocation, not an output wrapper.
// Copies of a Stage borrow this owner; replay never advances its fetched frontier.
// DML's separate candidate loops use the same owner before their predicates.
type RelationScan struct {
	source     *Source
	admission  *Source // Enclosing mixed hierarchy's borrowed statement authority.
	local      *relationScanCounts // Worker-owned scalars, reduced only after join.
	ordinary   relationScanProgress
	segments   []relationScanProgress
	pruned     bool
	crossed    int
	rangeStart int // Worker-local physical prefix, not fetched by this invocation.
	readSource bool // DML owns activation here; query stages own their own frontier.
}

type relationScanProgress struct {
	access  statistics.RelationAccess
	started bool
	fetched int
}

type relationScanCounts struct {
	starts, tuples int64
}

func NewRelationScan(source *Source) RelationScan {
	return RelationScan{source: source, readSource: true}
}

func (s *RelationScan) start(source *Source, p *relationScanProgress) {
	if p.started {
		return
	}
	p.started = true
	if source == nil || source.Anonymous != nil || source.Foreign != nil || source.Materialized || source.Final != nil || source.Input != nil || source.Singleton || source.NestedRoot != nil || source.RlsDenied {
		return
	}
	if s.readSource {
		source.beginRead()
	}
	if s.local != nil {
		s.local.starts++
		return
	}
	host := source.LiveHost()
	if host == nil && s.source != nil {
		// Physical hierarchy prefixes borrow the enclosing source's admission;
		// they do not own another statement host or a provider reference.
		host = s.source.LiveHost()
	}
	if host == nil && s.admission != nil {
		host = s.admission.LiveHost()
	}
	if host != nil {
		p.access = host.CaptureRelationAccess(source.Ref)
		p.access.Record(1, 0, 0)
	}
}

func (s *RelationScan) segment(index int) *relationScanProgress {
	if s.segments == nil {
		s.segments = make([]relationScanProgress, len(s.source.Inheritance))
		for i := range s.segments {
			start := 0
			if i > 0 {
				start = s.source.InheritanceEnds[i-1]
			}
			s.segments[i].fetched = max(0, s.rangeStart-start)
		}
	}
	p := &s.segments[index]
	s.start(s.source.Inheritance[index].Prefix, p)
	return p
}

// CrossEmpty records an explicitly reached empty physical branch. The pruner
// supplies identity when coalesced nonempty intervals cannot represent it.
func (s *RelationScan) CrossEmpty(index int) {
	if s == nil || s.source == nil {
		return
	}
	if s.source.Inheritance == nil {
		s.start(s.source, &s.ordinary)
		return
	}
	s.segment(index)
}

// Fetch is called only by the physical producer, before SQL/RLS filtering.
func (s *RelationScan) Fetch(ordinal int) {
	if s == nil || s.source == nil {
		return
	}
	p := &s.ordinary
	local := ordinal
	if s.source.Inheritance != nil {
		index := sort.SearchInts(s.source.InheritanceEnds, ordinal+1)
		p = s.segment(index)
		if index > 0 {
			local -= s.source.InheritanceEnds[index-1]
		}
	} else {
		s.start(s.source, p)
	}
	if local >= p.fetched {
		delta := int64(local+1-p.fetched)
		if s.local != nil {
			s.local.tuples += delta
		} else {
			p.access.Record(0, delta, 0)
		}
		p.fetched = local + 1
	}
}

// Reach traverses empty branches only as the sequential demand crosses them.
// The base producer calls this, never retained wrappers or index candidates.
func (s *RelationScan) Reach(ordinal int) {
	if s == nil || s.source == nil || s.pruned {
		return
	}
	if s.source.Inheritance == nil {
		s.start(s.source, &s.ordinary)
		return
	}
	for s.crossed < len(s.source.Inheritance) {
		start := 0
		if s.crossed > 0 {
			start = s.source.InheritanceEnds[s.crossed-1]
		}
		if start > ordinal {
			break
		}
		end := s.source.InheritanceEnds[s.crossed]
		if start == end && start >= s.rangeStart {
			s.CrossEmpty(s.crossed)
		}
		if end > ordinal {
			break
		}
		s.crossed++
	}
}

func (s *RelationScan) Drain(start, end int) {
	if s == nil || s.source == nil {
		return
	}
	if s.source.Inheritance == nil {
		s.Reach(start)
		if end > start {
			s.Fetch(end - 1)
		}
		return
	}
	for index, segmentEnd := range s.source.InheritanceEnds {
		segmentStart := 0
		if index > 0 {
			segmentStart = s.source.InheritanceEnds[index-1]
		}
		if segmentStart == segmentEnd && segmentStart >= start && segmentEnd <= end {
			s.CrossEmpty(index)
		}
		if lo, hi := max(start, segmentStart), min(end, segmentEnd); lo < hi {
			s.Fetch(hi - 1)
		}
	}
}
