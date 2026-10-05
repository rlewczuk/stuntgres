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
	"unicode/utf8"
)

// SettingDefinition is immutable shared metadata, never session value storage.
type SettingDefinition struct {
	Name        string
	BootDefault string
	Normalize   func(string) (string, *sqlstate.Error, error)
}

// SessionDefaultProvider is evaluated once per definition at physical
// initialization, in unspecified order. Absence selects that definition's boot
// default; DEFAULT restores the captured value without calling the provider again.
type SessionDefaultProvider interface {
	SessionDefault(context.Context, SettingDefinition) (string, bool, error)
}

var applicationName = SettingDefinition{Name: "application_name", Normalize: normalizeApplicationName}

func (e *Engine) settingDefinitions() map[string]SettingDefinition { return e.settingsDefinitions }
func (e *Engine) setting(name string) (SettingDefinition, error) {
	if d, ok := e.settingsDefinitions[strings.ToLower(name)]; ok {
		return d, nil
	}
	return SettingDefinition{}, sqlError("42704", fmt.Sprintf(`unrecognized configuration parameter "%s"`, name))
}
func normalizeApplicationName(value string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(value) {
		return "", nil, errors.New("stuntgres: invalid UTF-8 setting")
	}
	var notice *sqlstate.Error
	if len(value) > 63 {
		n := 63
		for !utf8.RuneStart(value[n]) {
			n--
		}
		notice = &sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, value, value[:n])}
		value = value[:n]
	}
	printable := true
	for i := range len(value) {
		if value[i] < 32 || value[i] > 126 {
			printable = false
			break
		}
	}
	if printable {
		return value, notice, nil
	}
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := range len(value) {
		c := value[i]
		if c >= 32 && c <= 126 {
			b.WriteByte(c)
		} else {
			b.WriteString(`\x`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String(), notice, nil
}

type sessionSettings struct {
	mu       sync.Mutex
	defaults map[string]string
	values   map[string]string
	ended    bool
}

func (s *sessionSettings) initialize(ctx context.Context, definitions map[string]SettingDefinition, provider SessionDefaultProvider) error {
	for _, d := range definitions {
		value := d.BootDefault
		if provider != nil {
			v, present, err := provider.SessionDefault(ctx, d)
			if err != nil {
				return err
			}
			if present {
				value = v
			}
		}
		normalized, _, err := d.Normalize(value)
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		s.defaults[d.Name] = normalized
	}
	return nil
}
func (s *sessionSettings) EndSession(_ SessionToken, _ EndReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	s.defaults = nil
	s.values = nil
	return nil
}
func (e *Engine) executeSetting(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	op := scope.op
	if count != 0 {
		return Result{}, errors.New("stuntgres: settings expect zero arguments")
	}
	var name string
	switch s := stmt.(type) {
	case *parser.Set:
		name = s.Name.Text
		if !s.Default && len(s.Values) != 1 {
			return Result{}, sqlError("22023", "SET "+name+" takes only one argument")
		}
	case *parser.Show:
		name = s.Name.Text
	}
	d, err := e.setting(name)
	if err != nil {
		return Result{}, err
	}
	var value string
	var notice *sqlstate.Error
	if s, ok := stmt.(*parser.Set); ok && !s.Default {
		value, notice, err = d.Normalize(s.Values[0])
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	owner := op.session.settings
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.ended {
		return Result{}, errors.New("stuntgres: settings attachment ended")
	}
	// Session utilities linearize with terminal admission before installing an
	// override or capturing SHOW, using Engine -> Session lock order.
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err = publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.Set:
		if s.Default {
			delete(owner.values, d.Name)
		} else {
			owner.values[d.Name] = value
			if notice != nil {
				op.emitNotice(levelNotice, *notice)
			}
		}
		scope.completed = true
		return Result{}, nil
	case *parser.Show:
		value, ok := owner.values[d.Name]
		if !ok {
			value = owner.defaults[d.Name]
		}
		typ, _ := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Text)
		result := Result{Columns: []ColumnDescriptor{{Name: d.Name, Usage: typ.Usage()}}, Rows: anonymousSnapshot([][]sqltype.Value{{sqltype.String(typ, value)}}, []int{0})}
		scope.completed = true
		return result, nil
	}
	return Result{}, errors.New("stuntgres: invalid setting statement")
}
