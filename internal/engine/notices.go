package engine

import (
	"fmt"
	"stuntgres/internal/sqlstate"
)

// noticeLevel values are ranks in settings message-level order.
type noticeLevel uint8

const (
	levelDebug5 noticeLevel = iota
	levelDebug4
	levelDebug3
	levelDebug2
	levelDebug1
	levelLog
	levelInfo
	levelNotice
	levelWarning
	levelError
)

// messageLevel maps a client message level name to its notice level. The
// noticeLevel constants follow the settings rank order.
func messageLevel(value string) (noticeLevel, bool) {
	rank, ok := messageLevelRank(value)
	return noticeLevel(rank), ok
}
func (op *Operation) snapshotNoticeThreshold() {
	s := op.session.settings
	s.mu.Lock()
	value, ok := s.values[clientMinMessages.Name]
	if !ok {
		value = s.defaults[clientMinMessages.Name]
	}
	s.mu.Unlock()
	op.noticeThreshold, _ = messageLevel(value)
}
func (op *Operation) emitNotice(level noticeLevel, diagnostic sqlstate.Error) {
	if level >= levelError || (level != levelInfo && level < op.noticeThreshold) {
		return
	}
	op.notices = append(op.notices, diagnostic)
}

// DetachNotices transfers owned records to the adapter, without invoking user code.
func (op *Operation) DetachNotices() []sqlstate.Error {
	notices := op.notices
	op.notices = nil
	return notices
}
func (op *Operation) parseNotice(diagnostic sqlstate.Error) {
	op.emitNotice(levelNotice, diagnostic)
}
func (op *Operation) existingRelationNotice(name string) {
	op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: fmt.Sprintf(`relation "%s" already exists, skipping`, name)})
}
