package session

import (
	"fmt"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// NoticeLevel values are ranks in settings message-level order.
type NoticeLevel uint8

const (
	LevelDebug5 NoticeLevel = iota
	LevelDebug4
	LevelDebug3
	LevelDebug2
	LevelDebug1
	LevelLog
	LevelInfo
	LevelNotice
	LevelWarning
	LevelError
)

// messageLevel maps a client message level name to its notice level. The
// NoticeLevel constants follow the settings rank order.
func messageLevel(value string) (NoticeLevel, bool) {
	rank, ok := settings.MessageLevelRank(value)
	return NoticeLevel(rank), ok
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
func (op *Operation) EmitNotice(level NoticeLevel, diagnostic sqlstate.Error) {
	if level >= LevelError || (level != LevelInfo && level < op.NoticeThreshold) {
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
	op.EmitNotice(LevelNotice, diagnostic)
}
func (op *Operation) ExistingRelationNotice(name string) {
	op.EmitNotice(LevelNotice, ExistingRelation(name))
}

// ExistingRelation is the NOTICE of CREATE ... IF NOT EXISTS for a relation
// name that is taken.
func ExistingRelation(name string) sqlstate.Error {
	return sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: fmt.Sprintf(`relation "%s" already exists, skipping`, name)}
}
