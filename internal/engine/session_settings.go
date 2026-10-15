package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"time"
)

type sessionSettings struct {
	mu               sync.Mutex
	session          *Session
	defaults, values map[string]string
	// typed contains only overrides; captured defaults never require reparsing.
	typed, defaultTyped map[string]settingValue
	placeholders        map[string]bool
	ended               bool
	actions             map[string]settingAction
	roleAction          roleSettingAction
	// Read-only projections of the transaction owner, published owner-first
	// under this mutex. These are never session overrides or journal actions.
	currentIsolation  string
	currentReadOnly   bool
	currentDeferrable bool
}

// Setting returns the current effective value of a recognized session setting.
// It reads only settings-protected state, including transaction projections.
func (s *Session) Setting(name string) (string, bool) {
	name = settingFold(name)
	settings := s.settings
	settings.mu.Lock()
	defer settings.mu.Unlock()
	if settings.ended {
		return "", false
	}
	_, hasDefault := settings.defaults[name]
	_, hasValue := settings.values[name]
	if !hasDefault && !hasValue && !settings.placeholders[name] {
		return "", false
	}
	return settings.value(name), true
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

// A role has one typed journal entry, not a generic string-setting override.
type roleSettingAction struct {
	present             bool
	original, candidate authorization.PrincipalRef
	kind                uint8
}

func roleRevisionHeadroom(a *sessionAuthorization, needed uint64) error {
	if ^uint64(0)-a.revision < needed {
		return fmt.Errorf("stuntgres: session authorization revision exhausted")
	}
	return nil
}

// Called with settings and Session locked. Role refs were validated before
// publication; restoration must not repeat membership eligibility checks.
func (s *sessionSettings) touchRole(current authorization.PrincipalRef, local bool) {
	a := s.roleAction
	if !a.present {
		a.present = true
		a.original = current
	}
	if !local {
		a.kind = settingSession
		a.candidate = authorization.PrincipalRef{}
	} else if !s.roleAction.present {
		a.kind = settingLocal
	} else if a.kind == settingSession {
		a.kind = settingSessionLocal
		a.candidate = current
	}
	s.roleAction = a
}

// Install a complete boundary state once; admission reserves revision capacity.
func (s *sessionSettings) restoreIdentityLocked(target authorization.RoleState) {
	a := s.session.authorization
	if a != nil && a.state != target {
		a.state = target
		a.revision++
	}
}

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

// finishTransaction runs under transaction admission. Typed timer restoration
// follows settings -> Session ordering, without providers or external callbacks.
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
	if s.session != nil {
		s.session.mu.Lock()
		t := s.session.transactions
		target := s.session.authorization.state
		if !commit && t.identityCaptured {
			target.Authenticated = t.identityEntry.Authenticated
			target.SessionUser = t.identityEntry.SessionUser
			target.ResetRole = t.identityEntry.ResetRole
		}
		if a := s.roleAction; a.present {
			if !commit || a.kind == settingLocal {
				target.CurrentUser = a.original
			} else if a.kind == settingSessionLocal {
				target.CurrentUser = a.candidate
			}
		}
		s.restoreIdentityLocked(target)
		t.identityEntry = authorization.RoleState{}
		t.identityCaptured = false
		s.session.publishTimeoutSettingsLocked(s, false, time.Now())
		s.session.mu.Unlock()
	}
	s.actions = nil
	s.roleAction = roleSettingAction{}
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
		if (d.Access == settingStartupOnly || d.Name == "server_encoding" || d.Name == "log_statement_stats") && normalized != d.BootDefault {
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
	s.roleAction = roleSettingAction{}
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
	if name == "transaction_deferrable" {
		if s.currentDeferrable {
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
func settingAccess(d SettingDefinition, op *Operation) error {
	switch d.Access {
	case settingStartupOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed without restarting the server`, d.Name))
	case SettingReadOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed`, d.Name))
	case SettingPrivileged:
		if d.Name == "lc_messages" {
			view, err := op.Authorization()
			if err != nil {
				return err
			}
			role, exists := view.roles.Find(view.state.CurrentUser)
			if exists && role.Superuser {
				return nil
			}
		}
		return sqlError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
	}
	return nil
}

// applySetting stages callbacks without publication locks, then atomically
// installs recognition, transaction action and effective typed/text override.
func (e *Engine) applySetting(op *Operation, owner *sessionSettings, d SettingDefinition, input *string, local bool) (string, error) {
	if d.Name == "transaction_isolation" || d.Name == "transaction_read_only" || d.Name == "transaction_deferrable" {
		return e.applyCurrentCharacteristic(op, d, input)
	}
	if err := settingAccess(d, op); err != nil {
		return "", err
	}
	var v settingValue
	var notice *sqlstate.Error
	if input != nil {
		var err error
		if d.Normalize != nil {
			v.text, notice, err = d.Normalize(*input)
		} else if d.Name == "datestyle" {
			owner.mu.Lock()
			current, ok := owner.typed[d.Name]
			if !ok {
				current = owner.defaultTyped[d.Name]
			}
			defaults := owner.defaultTyped[d.Name]
			owner.mu.Unlock()
			style, detail := sqltype.ParseDateStyle(*input, current.dateStyle, defaults.dateStyle)
			if detail != "" {
				err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "DateStyle": "%s"`, *input), Detail: detail}
			}
			v.dateStyle, v.text = style, style.String()
		} else {
			v, err = d.parse(*input)
		}
		if err == nil && localeSetting(d.Name) {
			v.locale, _ = sqltype.ParseCLocale(v.text)
		}
		if err != nil {
			if notice != nil {
				op.emitNotice(levelWarning, *notice)
			}
			return "", err
		}
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
	if d.Name == "transaction_timeout" || d.Name == "idle_in_transaction_session_timeout" || d.Name == "idle_session_timeout" {
		op.session.publishTimeoutSettingsLocked(owner, d.Name == "transaction_timeout", time.Now())
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
		if atom := s.Interval; atom != nil {
			precision := atom.Precision
			if precision > 6 {
				op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(" + strconv.FormatInt(int64(precision), 10) + ") precision reduced to maximum allowed, 6"})
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
			e.resetAllSettingsLocked(op.session)
			return Result{}, nil
		}
		rows := make([][]sqltype.Value, 0, len(e.settingsOrder))
		for _, key := range e.settingsOrder {
			if err := op.ctx.Err(); err != nil {
				return Result{}, err
			}
			d := e.settingsDefinitions[key]
			rows = append(rows, []sqltype.Value{sqltype.String(typ, settingDisplayName(key)), sqltype.String(typ, owner.value(key)), sqltype.String(typ, d.Description)})
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
	return anonymousResult(settingColumns(typ, settingDisplayName(d.Name)), [][]sqltype.Value{{sqltype.String(typ, owner.value(d.Name))}}, 0)
}
