package core

import "stuntgres/internal/engine/exec"

// Normal explicit/simple disposal transfers only the completed measurement to
// Finish. Publication or late terminal errors can still reject this command;
// neither a transport response nor an early result Close is success authority.
func (s *StatementScope) closeStatementStatsPortal(p *protocolPortal) error {
	frame := p.statementStats
	p.deferStatementStats = true
	err := p.close(s.Op.Context())
	p.deferStatementStats = false
	s.captureStatementStatsTerminal(frame)
	if frame != nil && !frame.finished {
		s.statementStatsCompletions = append(s.statementStatsCompletions, frame)
	}
	return err
}

func (s *StatementScope) closeStatementStatsCursor(c *sqlCursor) error {
	frame := c.statementStats
	c.deferStatementStats = true
	err := c.dispose(s.Op.Context())
	c.deferStatementStats = false
	s.captureStatementStatsTerminal(frame)
	if frame != nil && !frame.finished {
		s.statementStatsCompletions = append(s.statementStatsCompletions, frame)
	}
	return err
}

func (s *StatementScope) closeStatementStatsCursors(cursors []*sqlCursor) error {
	var failure error
	for _, cursor := range cursors {
		alreadyFailed := cursor.Place == exec.CursorFailed
		if err := s.closeStatementStatsCursor(cursor); failure == nil && !alreadyFailed {
			failure = err
		}
	}
	return failure
}
