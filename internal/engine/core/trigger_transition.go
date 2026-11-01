package core

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// A segment owns accepted immutable rows; only its append vectors and exposure
// bit are mutable. Checkpoints share row payloads, not append/freeze progress.
type transitionSegment struct {
	schema            plan.QuerySchema
	old, next         [][]sqltype.Value
	wantOld, wantNext bool
	frozen            bool
	statement         bool // A producer explicitly named this relation/event.
}

// Each accepted side is detached only on demand and shared by every consumer.
// A scalar ROW consumer may later demand the counterpart of a one-sided set.
type afterRowImages struct {
	old, next []sqltype.Value
}

func (images *afterRowImages) detach(old, next []sqltype.Value, wantOld, wantNext bool) {
	if wantOld && images.old == nil {
		images.old = slices.Clone(old)
	}
	if wantNext && images.next == nil {
		images.next = slices.Clone(next)
	}
}

func (s *StatementScope) registeredTransition(key afterStatementKey) *transitionSegment {
	if s.afterTriggers != nil {
		if frame := s.afterTriggers.frames[s.effectCommand]; frame != nil {
			if segment := frame.segments[key]; segment != nil && segment.statement {
				return segment
			}
		}
	}
	return nil
}

func (f *afterTriggerFrame) transition(key afterStatementKey, schema []catalog.Column, old, next, accepting bool) *transitionSegment {
	if f.segments == nil {
		f.segments = make(map[afterStatementKey]*transitionSegment)
	}
	segment := f.segments[key]
	if segment == nil || segment.frozen && accepting {
		segment = &transitionSegment{schema: schema, wantOld: old, wantNext: next}
		if prior := f.segments[key]; prior != nil {
			segment.statement = prior.statement
		}
		f.segments[key] = segment
	}
	return segment
}

func (s *transitionSegment) append(old, next []sqltype.Value) {
	if s.wantOld && old != nil {
		s.old = append(s.old, old)
	}
	if s.wantNext && next != nil {
		s.next = append(s.next, next)
	}
}

func (f *afterTriggerFrame) cloneTransitions() {
	if f.segments == nil {
		return
	}
	copies := make(map[*transitionSegment]*transitionSegment)
	clone := func(segment *transitionSegment) *transitionSegment {
		if segment == nil {
			return nil
		}
		if prior := copies[segment]; prior != nil {
			return prior
		}
		copy := *segment
		// Full slice expressions impose copy-on-append for either branch of a
		// checkpoint. Accepted payloads are never mutated by scans or producers.
		copy.old = segment.old[:len(segment.old):len(segment.old)]
		copy.next = segment.next[:len(segment.next):len(segment.next)]
		segment.old = copy.old
		segment.next = copy.next
		copies[segment] = &copy
		return &copy
	}
	f.segments = maps.Clone(f.segments)
	for key, segment := range f.segments {
		f.segments[key] = clone(segment)
	}
	for i := range f.rows {
		f.rows[i].segment = clone(f.rows[i].segment)
	}
	for i := range f.statements {
		f.statements[i].segment = clone(f.statements[i].segment)
	}
	for i := range f.producers {
		f.producers[i].candidates = slices.Clone(f.producers[i].candidates)
		for j := range f.producers[i].candidates {
			f.producers[i].candidates[j].segment = clone(f.producers[i].candidates[j].segment)
		}
		f.producers[i].segment = clone(f.producers[i].segment)
	}
}
