package core

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func tcnQuote(b *strings.Builder, text string, quote byte) {
	b.WriteByte(quote)
	for i := range len(text) {
		b.WriteByte(text[i])
		if text[i] == quote {
			b.WriteByte(quote)
		}
	}
	b.WriteByte(quote)
}

func (s *StatementScope) notifyTriggerChange(relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value) error {
	protocol := func(message string) error { return SQLError("39P01", "triggered_change_notification: "+message) }
	if p.Timing != stored.TriggerAfter {
		return protocol("must be called after the change")
	}
	if p.Level != stored.TriggerRow {
		return protocol("must be called for each row")
	}
	op, row := byte('I'), next
	switch event {
	case stored.TriggerInsert:
	case stored.TriggerUpdate:
		op, row = 'U', old
	case stored.TriggerDelete:
		op, row = 'D', old
	default:
		return fmt.Errorf("stuntgres: invalid tcn trigger event")
	}
	if len(p.Arguments) > 1 {
		return protocol("must not be called with more than one parameter")
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.namespaceAuthorizationLocked(e)
	var keys []catalog.IndexKey
	if err == nil {
		for _, constraint := range relation.Constraints.Unique {
			if !constraint.Primary {
				continue
			}
			index, ok := auth.Catalog.Relation(constraint.Index)
			if ok && index.SQLIndex != nil && index.SQLIndex.Valid && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Primary {
				keys = index.SQLIndex.Keys
				break
			}
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return protocol("must be called on a table with a primary key")
	}
	var payload strings.Builder
	tcnQuote(&payload, relation.Name(), '"')
	payload.WriteByte(',')
	payload.WriteByte(op)
	for _, key := range keys {
		slot := relation.IndexForAttribute(key.Attribute)
		if slot < 0 || slot >= len(row) {
			return fmt.Errorf("stuntgres: invalid tcn primary key attribute")
		}
		value := row[slot]
		text, err := value.Type().OutputWithEnvironment(s.executionContext(), value, s.Output)
		if err != nil {
			return err
		}
		payload.WriteByte(',')
		tcnQuote(&payload, relation.Columns[slot].Name, '"')
		payload.WriteByte('=')
		tcnQuote(&payload, text, '\'')
	}
	channel := "tcn"
	if len(p.Arguments) == 1 {
		channel = p.Arguments[0]
	}
	if channel == "" {
		return SQLError("22023", "channel name cannot be empty")
	}
	if len(channel) >= 64 {
		return SQLError("22023", "channel name too long")
	}
	if payload.Len() >= 8000 {
		return SQLError("22023", "payload string too long")
	}
	s.notificationEvents = append(s.notificationEvents, session.Notification{PID: uint64(s.Op.Session.SessionToken.ID()), Channel: channel, Payload: payload.String()})
	return nil
}
