package engine

import (
	"context"
	"errors"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// admitSettingsLocked checks that op is a live publication operation and that
// the store is still attached. Caller holds Settings.Mu then Session.Mu.
func admitSettingsLocked(s *session.SettingsStore, domain *ids.Domain, op *session.Operation) error {
	if err := publicationOperationDomainLocked(domain, op); err != nil {
		return err
	}
	if s.Ended {
		return errors.New("stuntgres: settings attachment ended")
	}
	return nil
}

// applySettingLocked atomically installs recognition, the transaction action
// and the effective typed/text override of d for op. A nil value resets d.
// Caller holds Engine.mu, Settings.Mu and op.Session.Mu, in that order.
func applySettingLocked(s *session.SettingsStore, op *session.Operation, domain *ids.Domain, registry settings.Registry, d settings.Definition, value *settings.Value, notice *sqlstate.Error, local bool) (string, error) {
	if err := admitSettingsLocked(s, domain, op); err != nil {
		return "", err
	}
	s.Touch(d.Name, local)
	if _, registered := registry.Definitions()[d.Name]; !registered {
		s.Placeholders[d.Name] = true
	}
	if value == nil {
		delete(s.Values, d.Name)
		delete(s.Typed, d.Name)
	} else {
		s.Values[d.Name] = d.Format(*value)
		s.Typed[d.Name] = *value
	}
	if d.Name == "transaction_timeout" || d.Name == "idle_in_transaction_session_timeout" || d.Name == "idle_session_timeout" {
		op.Session.PublishTimeoutSettingsLocked(s, d.Name == "transaction_timeout", time.Now())
	}
	if notice != nil {
		op.EmitNotice(session.LevelNotice, *notice)
	}
	return s.Value(d.Name), nil
}

// resetAllSettingsLocked resets every session-settable override. Defaults and
// placeholder recognition remain intact; the default provider is never
// invoked again. Caller holds Settings.Mu then Session.Mu of s.Session.
func resetAllSettingsLocked(s *session.SettingsStore, registry settings.Registry) {
	timeoutChanged, transactionAssignment := false, false
	for key := range s.Values {
		if d, ok := registry.Definitions()[key]; ok && d.Access != settings.Session && d.Name != "lc_messages" {
			continue
		}
		s.Touch(key, false)
		delete(s.Values, key)
		delete(s.Typed, key)
		switch key {
		case "transaction_timeout":
			timeoutChanged, transactionAssignment = true, true
		case "idle_in_transaction_session_timeout", "idle_session_timeout":
			timeoutChanged = true
		}
	}
	if timeoutChanged {
		s.Session.PublishTimeoutSettingsLocked(s, transactionAssignment, time.Now())
	}
}

// showAllSettingRowsLocked returns the SHOW ALL rows (name, setting,
// description) in registry order. Caller holds Settings.Mu.
func showAllSettingRowsLocked(s *session.SettingsStore, ctx context.Context, registry settings.Registry, typ *sqltype.Descriptor) ([][]sqltype.Value, error) {
	rows := make([][]sqltype.Value, 0, len(registry.Order()))
	for _, key := range registry.Order() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := registry.Definitions()[key]
		rows = append(rows, []sqltype.Value{sqltype.String(typ, settings.DisplayName(key)), sqltype.String(typ, s.Value(key)), sqltype.String(typ, d.Description)})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
