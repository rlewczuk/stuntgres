package engine

import (
	"fmt"

	"stuntgres/internal/engine/settings"
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
	rank, ok := settings.MessageLevelRank(value)
	return noticeLevel(rank), ok
}
func (op *Operation) SnapshotNoticeThreshold() {
	s := op.Session.Settings
	s.Mu.Lock()
	value, ok := s.Values[settings.ClientMinMessages.Name]
	if !ok {
		value = s.Defaults[settings.ClientMinMessages.Name]
	}
	s.Mu.Unlock()
	op.NoticeThreshold, _ = messageLevel(value)
}
func (op *Operation) EmitNotice(level noticeLevel, diagnostic sqlstate.Error) {
	if level >= levelError || (level != levelInfo && level < op.NoticeThreshold) {
		return
	}
	op.Notices = append(op.Notices, diagnostic)
}

// DetachNotices transfers owned records to the adapter, without invoking user code.
func (op *Operation) DetachNotices() []sqlstate.Error {
	notices := op.Notices
	op.Notices = nil
	return notices
}
func (op *Operation) ParseNotice(diagnostic sqlstate.Error) {
	op.EmitNotice(levelNotice, diagnostic)
}
func (op *Operation) ExistingRelationNotice(name string) {
	op.EmitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: fmt.Sprintf(`relation "%s" already exists, skipping`, name)})
}
