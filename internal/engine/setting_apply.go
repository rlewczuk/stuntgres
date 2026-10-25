package engine

import (
	"errors"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// applySetting stages callbacks without publication locks, then installs the
// parsed value in one Engine.mu -> Settings.Mu -> Session.Mu window.
func (e *Engine) applySetting(op *session.Operation, owner *session.SettingsStore, d settings.Definition, input *string, local bool) (string, error) {
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
		value, notice = &v, parseNotice
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	return owner.ApplyLocked(op, DomainOf(e), e.Settings, d, value, notice, local)
}

func (e *Engine) executeSetting(op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, errors.New("stuntgres: settings expect zero arguments")
	}
	owner := op.Session.Settings
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
			if err := op.Ctx.Err(); err != nil {
				return Result{}, err
			}
			owner.ResetAllLocked(e.Settings)
			return Result{}, nil
		}
		rows, err := owner.ShowAllRowsLocked(op.Ctx, e.Settings, typ)
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
		_, err = e.applySetting(op, owner, d, input, local)
		return Result{}, err
	}
	if err := op.Ctx.Err(); err != nil {
		return Result{}, err
	}
	return anonymousResult(settingColumns(typ, settings.DisplayName(d.Name)), [][]sqltype.Value{{sqltype.String(typ, owner.Value(d.Name))}}, 0)
}
