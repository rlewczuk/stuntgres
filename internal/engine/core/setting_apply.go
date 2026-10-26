package core

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// applySetting stages callbacks without publication locks, then installs the
// parsed value in one Engine.mu -> Settings.Mu -> Session.Mu window.
func (e *Engine) applySetting(ctx context.Context, op *session.Operation, owner *session.SettingsStore, d settings.Definition, input *string, local bool, resultName string) (string, error) {
	// All admitted definitions lack GUC_ALLOW_IN_PARALLEL. PostgreSQL's sole
	// flagged builtin, is_superuser, and function-local GUC_ACTION_SAVE are
	// not supported settings routes here. Revisit this when admitting either.
	// Resolve names first, but reject before access, parsing or installation.
	if exec.InParallelOperation(ctx) {
		return "", SQLError("25000", fmt.Sprintf(`parameter "%s" cannot be set during a parallel operation`, settings.DisplayName(d.Name)))
	}
	if err := restrictedParameter(op, d.Name); err != nil {
		return "", err
	}
	if d.Name == "transaction_isolation" || d.Name == "transaction_read_only" || d.Name == "transaction_deferrable" {
		return e.applyCurrentCharacteristic(op, d, input)
	}
	if err := settingAccess(d, op); err != nil {
		return "", err
	}
	var value *settings.Value
	var notice *sqlstate.Error
	if input != nil {
		v, parseNotice, err := owner.ParseValue(d, *input)
		if err != nil {
			if parseNotice != nil {
				op.EmitNotice(session.LevelWarning, *parseNotice)
			}
			return "", err
		}
		if err := checkClientSetting(op, d, &v); err != nil {
			if parseNotice != nil {
				op.EmitNotice(session.LevelNotice, *parseNotice)
			}
			return "", err
		}
		value, notice = &v, parseNotice
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	// set_config returns an examined value. PostgreSQL checks that after SET,
	// rolling the statement back on denial. Preflight it after validation but
	// before installation so the same error cannot leave an override behind.
	if resultName != "" && d.ReadRestricted {
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return "", err
		}
		allowed, err := auth.canReadRestrictedSettings()
		if err != nil {
			return "", err
		}
		if err := session.SettingReadAccess(d, resultName, allowed); err != nil {
			return "", err
		}
	}
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	result, err := owner.ApplyLocked(op, DomainOf(e), e.Settings, d, value, notice, local)
	if err == nil && op.Restricted != nil {
		if d.Name == "search_path" {
			op.Restricted.SafePath = false
		}
		op.RefreshInvocationSettingsLocked(owner)
	}
	return result, err
}

// SQL setting utilities are refused by PostgreSQL's utility admission before
// ExecSetVariableStmt. set_config reaches the named guard in applySetting.
func parallelSettingCommandEligibility(ctx context.Context, stmt parser.Statement) error {
	if exec.InParallelOperation(ctx) {
		return SQLError("25000", "cannot execute "+RoutineCommandTag(stmt)+" during a parallel operation")
	}
	return nil
}

func (e *Engine) executeSetting(ctx context.Context, op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, errors.New("stuntgres: settings expect zero arguments")
	}
	switch stmt.(type) {
	case *parser.Set, *parser.Reset:
		if err := parallelSettingCommandEligibility(ctx, stmt); err != nil {
			return Result{}, err
		}
	}
	owner := op.EffectiveSettings()
	var name string
	var input *string
	var all, write bool
	switch s := stmt.(type) {
	case *parser.Set:
		name = s.Name.Text
		write = true
		var err error
		if input, err = session.SetInput(op, s, implicit); err != nil {
			return Result{}, err
		}
	case *parser.Reset:
		name = s.Name.Text
		write = true
		all = s.All
	case *parser.Show:
		name = s.Name.Text
		all = s.All
	default:
		return Result{}, errors.New("stuntgres: invalid setting statement")
	}
	e.Mu.Lock()
	readRestricted := false
	if !write {
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		readRestricted, err = auth.canReadRestrictedSettings()
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	owner.Mu.Lock()
	op.Session.Mu.Lock()
	locked := true
	unlock := func() { op.Session.Mu.Unlock(); owner.Mu.Unlock(); e.Mu.Unlock() }
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err := owner.AdmitLocked(DomainOf(e), op); err != nil {
		return Result{}, err
	}
	typ, _ := e.Catalog.Current().LookupType(sqltype.Text)
	if all {
		if write {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			owner.ResetAllLocked(e.Settings)
			if op.Restricted != nil {
				op.Restricted.SafePath = false
				op.RefreshInvocationSettingsLocked(owner)
			}
			return Result{}, nil
		}
		rows, err := owner.ShowAllRowsLocked(ctx, e.Settings, typ, readRestricted)
		if err != nil {
			return Result{}, err
		}
		return anonymousResult(settingColumns(typ, "name", "setting", "description"), rows, 0)
	}
	d, err := owner.LookupDefinition(e.Settings, name, write)
	if err != nil {
		return Result{}, err
	}
	if write {
		local := false
		if s, ok := stmt.(*parser.Set); ok {
			local = s.Local
		}
		unlock()
		locked = false
		_, err = e.applySetting(ctx, op, owner, d, input, local, "")
		return Result{}, err
	}
	if err := session.SettingReadAccess(d, name, readRestricted); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return anonymousResult(settingColumns(typ, settings.DisplayName(d.Name)), [][]sqltype.Value{{sqltype.String(typ, owner.Value(d.Name))}}, 0)
}
