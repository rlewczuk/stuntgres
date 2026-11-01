package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

type AfterTriggerTestFrame struct {
	Rows, Statements, RowCursor, StatementCursor int
	Qualified, Skipped                           int
	Old, New                                     [][]sqltype.Value
	Targets, StatementTargets, ProducerTargets   [][]int32
	RowBacking, StatementBacking                 *afterTriggerEvent
	Segments                                     []AfterTransitionTestSegment
	RowSegments, StatementSegments               []*transitionSegment
	StatementEvents                              []stored.TriggerEvent
	RowRelations                                 []ids.ObjectRef
}

func ScopeAfterTestFrames(s *StatementScope) map[uint64]AfterTriggerTestFrame {
	frames := make(map[uint64]AfterTriggerTestFrame)
	if s.afterTriggers == nil {
		return frames
	}
	for id, f := range s.afterTriggers.frames {
		entry := AfterTriggerTestFrame{Rows: len(f.rows), Statements: len(f.statements), RowCursor: f.row, StatementCursor: f.statement}
		for key, segment := range f.segments {
			entry.Segments = append(entry.Segments, AfterTransitionTestSegment{Identity: segment, Schema: segment.schema, Old: segment.old, New: segment.next, Frozen: segment.frozen, Event: key.event})
		}
		for _, event := range f.rows {
			entry.Old = append(entry.Old, event.old)
			entry.New = append(entry.New, event.next)
		}
		for _, event := range f.rows {
			entry.Targets = append(entry.Targets, event.targets)
			entry.RowSegments = append(entry.RowSegments, event.segment)
			entry.RowRelations = append(entry.RowRelations, event.definition.Parts().Relation)
		}
		if len(f.rows) != 0 {
			entry.RowBacking = &f.rows[0]
		}
		if len(f.statements) != 0 {
			entry.StatementBacking = &f.statements[0]
		}
		for _, producer := range f.producers {
			for _, event := range producer.candidates {
				entry.ProducerTargets = append(entry.ProducerTargets, event.targets)
			}
		}
		for _, event := range f.statements {
			entry.StatementTargets = append(entry.StatementTargets, event.targets)
			entry.StatementSegments = append(entry.StatementSegments, event.segment)
			entry.StatementEvents = append(entry.StatementEvents, event.event)
			if event.qualified {
				entry.Qualified++
			}
			if event.skip {
				entry.Skipped++
			}
		}
		frames[id] = entry
	}
	return frames
}
func ScopeDrainAfterTest(s *StatementScope) error {
	if err := s.qualifyAfterStatements(); err != nil {
		return err
	}
	return s.drainAfter()
}
func ScopeEffectTestIDs(s *StatementScope) (uint64, uint64) { return s.effectCommand, s.effectSerial }
func ScopeDeferAfterProducersTest(s *StatementScope) func() {
	deferred, command := s.deferAfterProducers, s.deferredAfterCommand
	s.deferAfterProducers, s.deferredAfterCommand = true, s.effectCommand
	return func() { s.deferAfterProducers, s.deferredAfterCommand = deferred, command }
}

type AfterTransitionTestSegment struct {
	Identity *transitionSegment
	Schema   plan.QuerySchema
	Old, New [][]sqltype.Value
	Frozen   bool
	Event    stored.TriggerEvent
}

func ScopeInstallTransitionTest(s *StatementScope, ref ids.ObjectRef, event stored.TriggerEvent, oldName, newName string) func() {
	segment := s.afterTriggers.frames[s.effectCommand].segments[afterStatementKey{ref, event}]
	return s.installTriggerRelations(stored.TriggerParts{OldTable: oldName, NewTable: newName}, segment)
}
