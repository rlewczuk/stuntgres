package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) sessionSetting(s *session.SettingsStore, name string, create bool) (settings.Definition, error) {
	key := settings.FoldName(name)
	if d, ok := e.settings.Definitions()[key]; ok {
		return d, nil
	}
	if !s.Placeholders[key] {
		if !create || !strings.Contains(name, ".") {
			return e.settings.Lookup(name)
		}
		if !settings.CustomName(name) {
			return settings.Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: "Custom parameter names must be two or more simple identifiers separated by dots."}
		}
	}
	return settings.Definition{Name: key, Type: settings.String}, nil
}

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
		if d.Normalize != nil {
			v.Text, notice, err = d.Normalize(*input)
		} else if d.Name == "datestyle" {
			owner.Mu.Lock()
			current, ok := owner.Typed[d.Name]
			if !ok {
				current = owner.DefaultTyped[d.Name]
			}
			defaults := owner.DefaultTyped[d.Name]
			owner.Mu.Unlock()
			style, detail := sqltype.ParseDateStyle(*input, current.DateStyle, defaults.DateStyle)
			if detail != "" {
				err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "DateStyle": "%s"`, *input), Detail: detail}
			}
			v.DateStyle, v.Text = style, style.String()
		} else {
			v, err = d.Parse(*input)
		}
		if err == nil && settings.IsLocaleSetting(d.Name) {
			v.Locale, _ = sqltype.ParseCLocale(v.Text)
		}
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
		if s.Local && implicit {
			op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
		}
		if atom := s.Interval; atom != nil {
			precision := atom.Precision
			if precision > 6 {
				op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(" + strconv.FormatInt(int64(precision), 10) + ") precision reduced to maximum allowed, 6"})
				precision = 6
			}
			interval, err := sqltype.DecodeZoneInterval(atom.Text, atom.Qualifier, precision)
			if err != nil {
				return Result{}, err
			}
			value := "INTERVAL '" + interval.String() + "'"
			input = &value
		}
		if !s.Default && s.Interval == nil {
			if strings.EqualFold(name, "search_path") || strings.EqualFold(name, "datestyle") {
				value := strings.Join(s.Values, ", ")
				input = &value
			} else {
				if len(s.Values) != 1 {
					return Result{}, sqlError("22023", "SET "+name+" takes only one argument")
				}
				input = &s.Values[0]
			}
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
	d, err := e.sessionSetting(owner, name, write)
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
