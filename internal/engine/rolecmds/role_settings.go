package rolecmds

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteRoleSettings changes one immutable persisted row, not the issuer's
// physical settings. Scope writers wait without Engine.Mu, then decide again
// against the refreshed complete role record before constructing the candidate.
func ExecuteRoleSettings(e *core.Engine, scope *core.StatementScope, s *parser.AlterRoleSettings, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	v, err := e.AuthorizationLocked(op)
	var target role.RoleSettingsTarget
	if err == nil {
		target, err = role.PlanRoleSettingsTarget(v.View, s)
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if scope.LockTicket != nil {
		if err := scope.AcquireRoleSetting(target.Key); err != nil {
			return core.Result{}, err
		}
	}
	for {
		e.Mu.Lock()
		v, err = e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		if v.RoleRecord.Revision == ^uint64(0) {
			e.Mu.Unlock()
			return core.Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
		}
		fresh, err := role.PlanRoleSettingsTarget(v.View, s)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		// Lock admission never authorizes a same-name replacement principal.
		if fresh.Key != target.Key {
			e.Mu.Unlock()
			return core.Result{}, ids.ErrCatalogReplaced
		}
		row := v.RoleRecord.Settings.Lookup(fresh.Key)
		e.Mu.Unlock()
		noticeStart := len(op.Notices)
		next, err := roleSettingsRow(e, op, s, row)
		if err != nil {
			return core.Result{}, err
		}
		e.Mu.Lock()
		current, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		_, err = role.PlanRoleSettingsTarget(current.View, s)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		if current.RoleRecord != v.RoleRecord || current.Catalog.State() != v.Catalog.State() || current.State != v.State {
			e.Mu.Unlock()
			op.Notices = op.Notices[:noticeStart]
			continue
		}
		if err := op.Context().Err(); err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		if slices.Equal(row, next) {
			e.Mu.Unlock()
			return core.Result{}, nil
		}
		op.Session.Mu.Lock()
		err = core.PublicationOperationLocked(e, op)
		if err == nil && s.All && fresh.Database != nil {
			err = e.FenceRoleSettingDatabaseLocked(op, fresh.Key.Database, fresh.Database)
		}
		if err == nil {
			err = core.InstallOperationRolesLocked(e, op, current.RoleRecord.WithSettings(current.RoleRecord.Settings.WithRow(fresh.Key, next)))
		}
		op.Session.Mu.Unlock()
		e.Mu.Unlock()
		return core.Result{}, err
	}
}

// roleSettingsRow copies only the selected row and never installs live settings.
func roleSettingsRow(e *core.Engine, op *session.Operation, s *parser.AlterRoleSettings, row []access.RoleSetting) ([]access.RoleSetting, error) {
	if s.Reset != nil && s.Reset.All {
		var next []access.RoleSetting
		for _, entry := range row {
			allowed, err := e.PersistedSettingResetAllowed(op, entry.Name)
			if err != nil {
				return nil, err
			}
			if !allowed {
				next = append(next, entry)
			}
		}
		return next, nil
	}
	var name string
	var input *string
	var err error
	if s.Set != nil {
		name = s.Set.Name.Text
		if s.FromCurrent {
			var value string
			name, value, err = e.PersistedSettingCurrent(op, name)
			input = &value
		} else {
			input, err = session.SetInput(op, s.Set, false)
		}
	} else {
		name = s.Reset.Name.Text
	}
	if err != nil {
		return nil, err
	}
	// PostgreSQL does not inspect an unknown reset name without an exact row.
	if input == nil && len(row) == 0 {
		return row, nil
	}
	canonical, stored, err := e.ValidatePersistedSetting(op, name, input)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(row, func(entry access.RoleSetting) bool { return entry.Name == canonical })
	if input == nil {
		if index < 0 {
			return row, nil
		}
		next := slices.Clone(row)
		return slices.Delete(next, index, index+1), nil
	}
	next := slices.Clone(row)
	entry := access.RoleSetting{Name: canonical, Value: stored}
	if index < 0 {
		next = append(next, entry)
	} else {
		next[index] = entry
	}
	return next, nil
}
