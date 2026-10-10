package engine

import (
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

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

var clientMinMessages = SettingDefinition{Name: "client_min_messages", Type: SettingEnum, BootDefault: "notice", Description: "Sets the message levels that are sent to the client.", EnumValues: []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"}, Normalize: normalizeClientMinMessages}

func messageLevel(value string) (noticeLevel, bool) {
	switch value {
	case "debug5":
		return levelDebug5, true
	case "debug4":
		return levelDebug4, true
	case "debug3":
		return levelDebug3, true
	case "debug2", "debug":
		return levelDebug2, true
	case "debug1":
		return levelDebug1, true
	case "log":
		return levelLog, true
	case "info":
		return levelInfo, true
	case "notice":
		return levelNotice, true
	case "warning":
		return levelWarning, true
	case "error":
		return levelError, true
	}
	return 0, false
}
func normalizeClientMinMessages(value string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(value) {
		return "", nil, errors.New("stuntgres: invalid UTF-8 setting")
	}
	normalized := strings.ToLower(value)
	if _, ok := messageLevel(normalized); !ok {
		return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "client_min_messages": "%s"`, value), Hint: "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."}
	}
	if normalized == "debug" {
		normalized = "debug2"
	}
	return normalized, nil, nil
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
