package session

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// AdmitLocked checks that op is a live publication operation and that
// the store is still attached. Caller holds Settings.Mu then Session.Mu.
func (s *SettingsStore) AdmitLocked(domain *ids.Domain, op *Operation) error {
	if err := PublicationOperationLocked(domain, op); err != nil {
		return err
	}
	if s.Ended {
		return errors.New("stuntgres: settings attachment ended")
	}
	return nil
}

// ApplyLocked atomically installs recognition, the transaction action
// and the effective typed/text override of d for op. A nil value resets d.
// Caller holds Engine.Mu, Settings.Mu and op.Session.Mu, in that order.
func (s *SettingsStore) ApplyLocked(op *Operation, domain *ids.Domain, registry settings.Registry, d settings.Definition, value *settings.Value, notice *sqlstate.Error, local bool) (string, error) {
	if err := s.AdmitLocked(domain, op); err != nil {
		return "", err
	}
	if err := s.checkStatistics(d.Name, value); err != nil {
		return "", err
	}
	if d.Name == "temp_buffers" && value != nil {
		if initialized := atomic.LoadInt64(&s.temporaryBufferAuthority().temporaryBuffers); initialized != 0 && int64(value.Number) != initialized {
			return "", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023",
				Message: fmt.Sprintf(`invalid value for parameter "temp_buffers": %d`, int64(value.Number)),
				Detail:  `"temp_buffers" cannot be changed after any temporary tables have been accessed in the session.`}
		}
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
	if !s.Invocation && (d.Name == "transaction_timeout" || d.Name == "idle_in_transaction_session_timeout" || d.Name == "idle_session_timeout") {
		op.Session.PublishTimeoutSettingsLocked(s, d.Name == "transaction_timeout", time.Now())
	}
	if notice != nil {
		op.EmitNotice(LevelNotice, *notice)
	}
	return s.Value(d.Name), nil
}

// ResetAllLocked resets every session-settable override. Defaults and
// placeholder recognition remain intact; the default provider is never
// invoked again. Caller holds Settings.Mu then Session.Mu of s.Session.
func (s *SettingsStore) ResetAllLocked(registry settings.Registry) {
	timeoutChanged, transactionAssignment := false, false
	for key := range s.Values {
		if d, ok := registry.Definitions()[key]; ok && d.Access != settings.Session && !(d.Access == settings.Privileged && parameterGrantSetting(d.Name)) {
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
	if timeoutChanged && !s.Invocation {
		s.Session.PublishTimeoutSettingsLocked(s, transactionAssignment, time.Now())
	}
}

// ShowAllRowsLocked returns the SHOW ALL rows (name, setting,
// description) in registry order, omitting restricted rows unless readRestricted
// grants captured effective-user authority. Caller holds Settings.Mu.
func (s *SettingsStore) ShowAllRowsLocked(ctx context.Context, registry settings.Registry, typ *sqltype.Descriptor, readRestricted bool) ([][]sqltype.Value, error) {
	rows := make([][]sqltype.Value, 0, len(registry.Order()))
	for _, key := range registry.Order() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := registry.Definitions()[key]
		if d.ReadRestricted && !readRestricted {
			continue
		}
		rows = append(rows, []sqltype.Value{sqltype.String(typ, settings.DisplayName(key)), sqltype.String(typ, s.Value(key)), sqltype.String(typ, d.Description)})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
