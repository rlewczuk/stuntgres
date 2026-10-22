package engine

import (
	"errors"
	"time"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// applySetting stages callbacks without publication locks, then atomically
// installs recognition, transaction action and effective typed/text override.
func (e *Engine) applySetting(op *session.Operation, owner *session.SettingsStore, d settings.Definition, input *string, local bool) (string, error) {
	if d.Name == "transaction_isolation" || d.Name == "transaction_read_only" || d.Name == "transaction_deferrable" {
		return e.applyCurrentCharacteristic(op, d, input)
	}
	if err := settingAccess(d, op); err != nil {
		return "", err
	}
	var v settings.Value
	var notice *sqlstate.Error
	if input != nil {
		var err error
		v, notice, err = owner.ParseValue(d, *input)
		if err != nil {
			if notice != nil {
				op.EmitNotice(session.LevelWarning, *notice)
			}
			return "", err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return "", err
	}
	if owner.Ended {
		return "", errors.New("stuntgres: settings attachment ended")
	}
	owner.Touch(d.Name, local)
	if _, registered := e.settings.Definitions()[d.Name]; !registered {
		owner.Placeholders[d.Name] = true
	}
	if input == nil {
		delete(owner.Values, d.Name)
		delete(owner.Typed, d.Name)
	} else {
		owner.Values[d.Name] = d.Format(v)
		owner.Typed[d.Name] = v
	}
	if d.Name == "transaction_timeout" || d.Name == "idle_in_transaction_session_timeout" || d.Name == "idle_session_timeout" {
		op.Session.PublishTimeoutSettingsLocked(owner, d.Name == "transaction_timeout", time.Now())
	}
	if notice != nil {
		op.EmitNotice(session.LevelNotice, *notice)
	}
	return owner.Value(d.Name), nil
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
	e.mu.Lock()
	owner.Mu.Lock()
	op.Session.Mu.Lock()
	locked := true
	unlock := func() { op.Session.Mu.Unlock(); owner.Mu.Unlock(); e.mu.Unlock() }
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if owner.Ended {
		return Result{}, errors.New("stuntgres: settings attachment ended")
	}
	typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Text)
	if all {
		if write {
			if err := op.Ctx.Err(); err != nil {
				return Result{}, err
			}
			e.resetAllSettingsLocked(op.Session)
			return Result{}, nil
		}
		rows := make([][]sqltype.Value, 0, len(e.settings.Order()))
		for _, key := range e.settings.Order() {
			if err := op.Ctx.Err(); err != nil {
				return Result{}, err
			}
			d := e.settings.Definitions()[key]
			rows = append(rows, []sqltype.Value{sqltype.String(typ, settings.DisplayName(key)), sqltype.String(typ, owner.Value(key)), sqltype.String(typ, d.Description)})
		}
		if err := op.Ctx.Err(); err != nil {
			return Result{}, err
		}
		return anonymousResult(settingColumns(typ, "name", "setting", "description"), rows, 0)
	}
	d, err := owner.LookupDefinition(e.settings, name, write)
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
