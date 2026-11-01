package core

import (
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// InitializeIsn is reached only after authentic installed provider admission.
// Recognition has physical-session lifetime; promotion reuses its setting image
// and journal, while notices are queued only after publication locks release.
func (h *statementHost) InitializeIsn() error {
	s := h.scope()
	op, e := s.Op, s.Engine()
	if err := s.executionContext().Err(); err != nil {
		return err
	}
	owner := op.EffectiveSettings()
	e.Mu.Lock()
	owner.Mu.Lock()
	op.Session.Mu.Lock()
	if err := owner.AdmitLocked(DomainOf(e), op); err != nil {
		op.Session.Mu.Unlock()
		owner.Mu.Unlock()
		e.Mu.Unlock()
		return err
	}
	loaded, before := owner.IsnLoaded(), owner.Value("isn.weak")
	warning := owner.LoadIsnLocked()
	changed := !loaded || before != owner.Value("isn.weak")
	op.RefreshInvocationSettingsLocked(owner)
	op.Session.Mu.Unlock()
	owner.Mu.Unlock()
	e.Mu.Unlock()
	if warning != nil {
		op.EmitNotice(session.LevelWarning, *warning)
	}
	if changed {
		s.presentationSlot = nil
		return s.refreshRestrictedPresentation()
	}
	return nil
}

func (h *statementHost) InvokeIsnWeak(on, set bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	if result == nil || result.ID() != sqltype.Bool {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid ISN weak result")
	}
	if err := h.InitializeIsn(); err != nil {
		return sqltype.Value{}, err
	}
	s := h.scope()
	owner := s.Op.EffectiveSettings()
	if !set {
		owner.Mu.Lock()
		enabled := owner.Value("isn.weak") == "on"
		owner.Mu.Unlock()
		return sqltype.Boolean(result, enabled), nil
	}
	input := "off"
	if on {
		input = "on"
	}
	if _, err := s.Engine().applySetting(s.executionContext(), s.Op, owner, session.IsnDefinition(), &input, session.SettingSet, ""); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(result, on), nil
}
