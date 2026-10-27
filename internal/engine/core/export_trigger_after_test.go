package core

import "stuntgres/internal/sqltype"

type AfterTriggerTestFrame struct {
	Rows, Statements, RowCursor, StatementCursor int
	Qualified, Skipped int
	Old, New [][]sqltype.Value
}
func ScopeAfterTestFrames(s *StatementScope) map[uint64]AfterTriggerTestFrame {
	frames := make(map[uint64]AfterTriggerTestFrame)
	if s.afterTriggers == nil { return frames }
	for id, f := range s.afterTriggers.frames {
		entry := AfterTriggerTestFrame{Rows:len(f.rows), Statements:len(f.statements), RowCursor:f.row, StatementCursor:f.statement}
		for _, event := range f.rows { entry.Old=append(entry.Old,event.old); entry.New=append(entry.New,event.next) }
		for _, event := range f.statements {
			if event.qualified { entry.Qualified++ }
			if event.skip { entry.Skipped++ }
		}
		frames[id]=entry
	}
	return frames
}
func ScopeDrainAfterTest(s *StatementScope) error {
	if err := s.qualifyAfterStatements(); err != nil { return err }
	return s.drainAfter()
}
func ScopeEffectTestIDs(s *StatementScope) (uint64,uint64) { return s.effectCommand,s.effectSerial }
