package core

import (
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

// InvokeSetting applies canonical setting expressions under live authority.
// Arguments have already been evaluated under the caller.
func (h *statementHost) InvokeSetting(name string, inputs []sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	op, e := s.Op, s.Engine()
	if name != "current_setting" && name != "set_config" && name != "pg_settings_get_flags" || name == "current_setting" && (len(inputs) < 1 || len(inputs) > 2) || name == "set_config" && len(inputs) != 3 || name == "pg_settings_get_flags" && len(inputs) != 1 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid setting expression signature")
	}
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	readRestricted := false
	if name == "current_setting" {
		readRestricted, err = auth.canReadRestrictedSettings()
		if err != nil {
			e.Mu.Unlock()
			return sqltype.Value{}, err
		}
	}
	owner := op.EffectiveSettings()
	owner.Mu.Lock()
	op.Session.Mu.Lock()
	unlock := func() { op.Session.Mu.Unlock(); owner.Mu.Unlock(); e.Mu.Unlock() }
	if err := owner.AdmitLocked(DomainOf(e), op); err != nil {
		unlock()
		return sqltype.Value{}, err
	}
	if name == "pg_settings_get_flags" {
		defer unlock()
		if inputs[0].IsNull() {
			return sqltype.Null(result), nil
		}
		requested, err := inputs[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		flags, exists := owner.SettingFlagsLocked(e.Settings, requested)
		if !exists {
			return sqltype.Null(result), nil
		}
		return flags.Array(result)
	}
	if name == "current_setting" {
		defer unlock()
		missingOK := false
		if len(inputs) == 2 {
			if inputs[1].IsNull() {
				return sqltype.Null(result), nil
			}
			missingOK, err = inputs[1].Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if !inputs[0].IsNull() {
			requested, err := inputs[0].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
			if settings.FoldName(requested) == "role" {
				value, err := roleSettingValue(auth)
				if err != nil {
					return sqltype.Value{}, err
				}
				return sqltype.String(result, value), nil
			}
		}
		value, err := owner.CurrentSettingLocked(s.executionContext(), e.Settings, result, inputs[0], missingOK, readRestricted)
		if err != nil || value.IsNull() {
			return value, err
		}
		requested, err := inputs[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if settings.FoldName(requested) != "is_superuser" {
			return value, nil
		}
		text, err := auth.settingValue(owner, settings.FoldName(requested))
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(result, text), nil
	}
	// Identity parameters have operation-local restrictions even though they
	// are not ordinary registry entries. Preserve that diagnostic before lookup.
	if !inputs[0].IsNull() {
		requested, err := inputs[0].Text()
		if err != nil {
			unlock()
			return sqltype.Value{}, err
		}
		if err := restrictedParameter(op, requested); err != nil {
			unlock()
			return sqltype.Value{}, err
		}
	}
	args := [3]sqltype.Value{inputs[0], inputs[1], inputs[2]}
	definition, input, local, err := owner.SetConfigInputLocked(e.Settings, args)
	if err != nil {
		unlock()
		return sqltype.Value{}, err
	}
	requested, err := inputs[0].Text()
	unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := e.applySetting(s.executionContext(), op, owner, definition, input, session.SettingAssignment(local), requested)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, value), nil
}
