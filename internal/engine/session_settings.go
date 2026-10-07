package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
)

type sessionSettings struct {
	mu               sync.Mutex
	defaults, values map[string]string
	// typed contains only overrides; captured defaults never require reparsing.
	typed, defaultTyped map[string]settingValue
	placeholders        map[string]bool
	ended               bool
	actions             map[string]settingAction
	// Read-only projections of the transaction owner, published owner-first
	// under this mutex. These are never session overrides or journal actions.
	currentIsolation string
	currentReadOnly  bool
}

type settingOverride struct {
	present bool
	text    string
	typed   settingValue
}
type settingAction struct {
	original  settingOverride
	candidate settingOverride
	kind      uint8
}

const (
	settingSession uint8 = iota
	settingLocal
	settingSessionLocal
)

func (s *sessionSettings) override(name string) settingOverride {
	text, present := s.values[name]
	return settingOverride{present: present, text: text, typed: s.typed[name]}
}
func (s *sessionSettings) restore(name string, v settingOverride) {
	if v.present {
		s.values[name] = v.text
		s.typed[name] = v.typed
	} else {
		delete(s.values, name)
		delete(s.typed, name)
	}
}
func (s *sessionSettings) touch(name string, local bool) {
	if s.actions == nil {
		s.actions = make(map[string]settingAction)
	}
	a, exists := s.actions[name]
	if !exists {
		a.original = s.override(name)
	}
	if !local {
		a.kind = settingSession
		a.candidate = settingOverride{}
	} else if !exists {
		a.kind = settingLocal
	} else if a.kind == settingSession {
		a.kind = settingSessionLocal
		a.candidate = s.override(name)
	}
	s.actions[name] = a
}

// finishTransaction runs under transaction admission; restoration never calls
// providers, normalizers, parsers, or session/catalog publication locks.
func (s *sessionSettings) finishTransaction(commit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		for name, a := range s.actions {
			if !commit || a.kind == settingLocal {
				s.restore(name, a.original)
			} else if a.kind == settingSessionLocal {
				s.restore(name, a.candidate)
			}
		}
	}
	s.actions = nil
}

func (s *sessionSettings) initialize(ctx context.Context, definitions map[string]SettingDefinition, names []string, provider SessionDefaultProvider) error {
	s.typed = make(map[string]settingValue)
	s.defaultTyped = make(map[string]settingValue)
	s.placeholders = make(map[string]bool)
	for _, name := range names {
		d := definitions[name]
		v := d.bootValue
		normalized := d.BootDefault
		if provider != nil {
			copy := d
			copy.EnumValues = append([]string(nil), d.EnumValues...)
			input, present, err := provider.SessionDefault(ctx, copy)
			if err != nil {
				return err
			}
			if present {
				v, err = d.parse(input)
				if err != nil {
					return err
				}
				normalized = d.format(v)
			}
		}
		if (d.Name == "server_encoding" || d.Name == "log_statement_stats") && normalized != d.BootDefault {
			return RegistrationError("default provider cannot change " + d.Name)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.defaults[d.Name] = normalized
		s.defaultTyped[d.Name] = v
	}
	return nil
}
func (s *sessionSettings) EndSession(_ SessionToken, _ EndReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	s.defaults = nil
	s.values = nil
	s.typed = nil
	s.defaultTyped = nil
	s.placeholders = nil
	s.actions = nil
	return nil
}
func (s *sessionSettings) value(name string) string {
	if name == "transaction_isolation" && s.currentIsolation != "" {
		return s.currentIsolation
	}
	if name == "transaction_read_only" {
		if s.currentReadOnly {
			return "on"
		}
		return "off"
	}
	if v, ok := s.values[name]; ok {
		return v
	}
	return s.defaults[name]
}
func (s *sessionSettings) typedValue(name string) settingValue {
	if v, ok := s.typed[name]; ok {
		return v
	}
	return s.defaultTyped[name]
}
func (e *Engine) sessionSetting(s *sessionSettings, name string, create bool) (SettingDefinition, error) {
	key := settingFold(name)
	if d, ok := e.settingsDefinitions[key]; ok {
		return d, nil
	}
	if !s.placeholders[key] {
		if !create || !strings.Contains(name, ".") {
			return e.setting(name)
		}
		if !customName(name) {
			return SettingDefinition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: fmt.Sprintf(`invalid configuration parameter name "%s"`, name), Detail: "Custom parameter names must be two or more simple identifiers separated by dots."}
		}
	}
	return SettingDefinition{Name: key, Type: SettingString}, nil
}
func settingAccess(d SettingDefinition) error {
	switch d.Access {
	case SettingReadOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed`, d.Name))
	case SettingPrivileged:
		return sqlError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
	}
	return nil
}

// applySetting stages callbacks without publication locks, then atomically
// installs recognition, transaction action and effective typed/text override.
func (e *Engine) applySetting(op *Operation, owner *sessionSettings, d SettingDefinition, input *string, local bool) (string, error) {
	if d.Name == "transaction_isolation" || d.Name == "transaction_read_only" {
		return e.applyCurrentCharacteristic(op, d, input)
	}
	if err := settingAccess(d); err != nil {
		return "", err
	}
	var v settingValue
	var notice *sqlstate.Error
	if input != nil {
		var err error
		if d.Normalize != nil {
			v.text, notice, err = d.Normalize(*input)
		} else {
			v, err = d.parse(*input)
		}
		if err != nil {
			return "", err
		}
	}
	if d.Name == "default_transaction_isolation" && input != nil && d.format(v) == "serializable" {
		return "", sqlError("0A000", "transaction mode is unsupported")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return "", err
	}
	if owner.ended {
		return "", errors.New("stuntgres: settings attachment ended")
	}
	owner.touch(d.Name, local)
	if _, registered := e.settingsDefinitions[d.Name]; !registered {
		owner.placeholders[d.Name] = true
	}
	if input == nil {
		delete(owner.values, d.Name)
		delete(owner.typed, d.Name)
	} else {
		owner.values[d.Name] = d.format(v)
		owner.typed[d.Name] = v
	}
	if notice != nil {
		op.emitNotice(levelNotice, *notice)
	}
	return owner.value(d.Name), nil
}

func (e *Engine) executeSetting(op *Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, errors.New("stuntgres: settings expect zero arguments")
	}
	owner := op.session.settings
	var name string
	var input *string
	var all, write bool
	switch s := stmt.(type) {
	case *parser.Set:
		name = s.Name.Text
		write = true
		if s.Local && implicit {
			op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
		}
		if !s.Default {
			if strings.EqualFold(name, "search_path") {
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
	owner.mu.Lock()
	op.session.mu.Lock()
	locked := true
	unlock := func() { op.session.mu.Unlock(); owner.mu.Unlock(); e.mu.Unlock() }
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if owner.ended {
		return Result{}, errors.New("stuntgres: settings attachment ended")
	}
	typ, _ := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Text)
	if all {
		if write {
			if err := op.ctx.Err(); err != nil {
				return Result{}, err
			}
			for key := range owner.values {
				if d, ok := e.settingsDefinitions[key]; ok && d.Access != SettingSession {
					continue
				}
				owner.touch(key, false)
				delete(owner.values, key)
				delete(owner.typed, key)
			}
			return Result{}, nil
		}
		rows := make([][]sqltype.Value, 0, len(e.settingsOrder))
		for _, key := range e.settingsOrder {
			if err := op.ctx.Err(); err != nil {
				return Result{}, err
			}
			d := e.settingsDefinitions[key]
			rows = append(rows, []sqltype.Value{sqltype.String(typ, key), sqltype.String(typ, owner.value(key)), sqltype.String(typ, d.Description)})
		}
		if err := op.ctx.Err(); err != nil {
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
	if err := op.ctx.Err(); err != nil {
		return Result{}, err
	}
	return anonymousResult(settingColumns(typ, d.Name), [][]sqltype.Value{{sqltype.String(typ, owner.value(d.Name))}}, 0)
}
