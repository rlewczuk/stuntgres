package core

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// persistedDefinition uses the session's existing placeholder resolver without
// creating a placeholder or changing any live setting state.
func (e *Engine) persistedDefinition(op *session.Operation, name string) (settings.Definition, error) {
	owner := op.EffectiveSettings()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	return owner.LookupDefinition(e.Settings, name, true)
}

// PersistedSettingPermission checks only context and the operation's current
// parameter authority. Callers repeat this after waits, outside publication locks.
func (e *Engine) PersistedSettingPermission(op *session.Operation, name string) error {
	name = settings.FoldName(name)
	if name == "role" {
		return restrictedParameter(op, name)
	}
	d, err := e.persistedDefinition(op, name)
	if err != nil {
		return err
	}
	if _, registered := e.Settings.Definitions()[name]; !registered {
		allowed, err := parameterSetAllowed(op, name)
		if err != nil {
			return err
		}
		if !allowed {
			return SQLError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, name))
		}
		return nil
	}
	return settingAccess(d, op)
}

// ValidatePersistedSetting extracts a catalog value without installing it in the
// issuer. A nil input validates RESET/DEFAULT; absent-row bypass belongs to the
// scope-row owner. Stored text is extracted SQL input, not SHOW formatting.
func (e *Engine) ValidatePersistedSetting(op *session.Operation, name string, input *string) (string, string, error) {
	name = settings.FoldName(name)
	if name == "role" {
		if err := e.PersistedSettingPermission(op, name); err != nil {
			return "", "", err
		}
		if input == nil {
			return name, "", nil
		}
		candidate, notice, err := settings.NormalizeCatalogName(*input)
		if err != nil {
			return "", "", err
		}
		if notice != nil {
			op.EmitNotice(session.LevelNotice, *notice)
		}
		if candidate != "none" {
			auth, err := OperationAuthorization(op)
			if err != nil {
				return "", "", err
			}
			if target, exists := auth.Roles.Lookup(candidate); !exists {
				op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42704", Message: fmt.Sprintf(`role "%s" does not exist`, candidate)})
			} else {
				allowed, err := auth.Roles.CanSetRole(auth.State.SessionUser, target.Ref)
				if err != nil {
					return "", "", err
				}
				if !allowed {
					op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42501", Message: fmt.Sprintf(`permission will be denied to set role "%s"`, candidate)})
				}
			}
		}
		return name, *input, nil
	}
	d, err := e.persistedDefinition(op, name)
	if err != nil {
		return "", "", err
	}
	if err = e.PersistedSettingPermission(op, name); err != nil {
		return "", "", err
	}
	if input == nil {
		return d.Name, "", nil
	}
	value, notice, err := op.EffectiveSettings().ParseValue(d, *input)
	if err != nil {
		if notice != nil {
			op.EmitNotice(session.LevelWarning, *notice)
		}
		return "", "", err
	}
	if err = checkClientSetting(op, d, &value); err != nil {
		return "", "", err
	}
	owner := op.EffectiveSettings()
	owner.Mu.Lock()
	err = owner.ValidateValueLocked(d, &value)
	owner.Mu.Unlock()
	if err != nil {
		return "", "", err
	}
	if notice != nil {
		op.EmitNotice(session.LevelNotice, *notice)
	}
	return d.Name, *input, nil
}

// roleSettingValue reads the captured physical role GUC, never the effective
// invocation actor or a live descriptor name.
func roleSettingValue(auth AuthorizationView) (string, error) {
	state := auth.SessionRoleState
	if !state.ActiveRole || state.RoleText == "" {
		return "none", nil
	}
	return state.RoleText, nil
}

// PersistedSettingCurrent captures the executing invocation's effective setting,
// including protected-read authority, without modifying the physical reset image.
func (e *Engine) PersistedSettingCurrent(op *session.Operation, name string) (string, string, error) {
	key := settings.FoldName(name)
	auth, err := OperationAuthorization(op)
	if err != nil {
		return "", "", err
	}
	if key == "role" {
		value, err := roleSettingValue(auth)
		return key, value, err
	}
	d, err := e.persistedDefinition(op, name)
	if err != nil {
		return "", "", err
	}
	allowed, err := auth.canReadRestrictedSettings()
	if err != nil {
		return "", "", err
	}
	if err = session.SettingReadAccess(d, name, allowed); err != nil {
		return "", "", err
	}
	owner := op.EffectiveSettings()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	if _, registered := e.Settings.Definitions()[key]; !registered && !owner.Placeholders[key] {
		_, err = e.Settings.Lookup(name)
		return "", "", err
	}
	return d.Name, owner.Value(d.Name), nil
}

// PersistedSettingResetAllowed differs deliberately from live RESET ALL: a
// revoked grant leaves the administrator-authored scope entry intact.
func (e *Engine) PersistedSettingResetAllowed(op *session.Operation, name string) (bool, error) {
	err := e.PersistedSettingPermission(op, name)
	if err == nil {
		return true, nil
	}
	var state *sqlstate.Error
	if errors.As(err, &state) && (state.Code == "42501" || state.Code == "55P02" || state.Code == "42704") {
		return false, nil
	}
	return false, err
}
