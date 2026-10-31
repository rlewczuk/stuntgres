package nativetrig

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/tsearch"
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

func notifyTriggerChange[A tsearch.Authority[A]](h Host[A], relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value) error {
	protocol := func(message string) error { return sqlError("39P01", "triggered_change_notification: "+message) }
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
	h.ReacquireEngine()
	auth, err := h.NamespaceAuthorizationLocked()
	var keys []catalog.IndexKey
	if err == nil {
		read := auth.Access().Catalog
		for _, constraint := range relation.Constraints.Unique {
			if !constraint.Primary {
				continue
			}
			index, ok := read.Relation(constraint.Index)
			if ok && index.SQLIndex != nil && index.SQLIndex.Valid && index.SQLIndex.Constraint != nil && index.SQLIndex.Constraint.Primary {
				keys = index.SQLIndex.Keys
				break
			}
		}
	}
	h.ReleaseEngineLocked()
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
	ctx := h.Context()
	_, output := h.Environments()
	for _, key := range keys {
		slot := relation.IndexForAttribute(key.Attribute)
		if slot < 0 || slot >= len(row) {
			return fmt.Errorf("stuntgres: invalid tcn primary key attribute")
		}
		value := row[slot]
		text, err := value.Type().OutputWithEnvironment(ctx, value, output)
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
		return sqlError("22023", "channel name cannot be empty")
	}
	if len(channel) >= 64 {
		return sqlError("22023", "channel name too long")
	}
	if payload.Len() >= 8000 {
		return sqlError("22023", "payload string too long")
	}
	h.QueueNotification(channel, payload.String())
	return nil
}
