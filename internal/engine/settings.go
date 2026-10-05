package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"sync"
	"unicode/utf8"
)

// SettingDefinition is immutable shared metadata, never session value storage.
type SettingDefinition struct {
	Name        string
	BootDefault string
	Normalize   func(string) (string, error)
	Column      parser.Column
}

// SessionDefaultProvider is evaluated once at physical initialization. Absence
// selects the definition's boot default; DEFAULT restores that captured value.
type SessionDefaultProvider interface {
	SessionDefault(context.Context, SettingDefinition) (string, bool, error)
}

var applicationName = SettingDefinition{Name: "application_name", Normalize: normalizeApplicationName, Column: parser.Column{Name: "application_name", Type: parser.Text}}

func (e *Engine) settingDefinitions() map[string]SettingDefinition { return e.settingsDefinitions }
func (e *Engine) setting(name string) (SettingDefinition, error) {
	if d, ok := e.settingsDefinitions[strings.ToLower(name)]; ok {
		return d, nil
	}
	return SettingDefinition{}, sqlError("42704", fmt.Sprintf(`unrecognized configuration parameter "%s"`, name))
}
func normalizeApplicationName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("stuntgres: invalid UTF-8 setting")
	}
	if len(value) > 63 {
		n := 63
		for !utf8.RuneStart(value[n]) {
			n--
		}
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
		return value, nil
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
	return b.String(), nil
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
		normalized, err := d.Normalize(value)
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
func (e *Engine) executeSetting(op *Operation, stmt parser.Statement, args []driver.NamedValue) (Result, error) {
	if len(args) != 0 {
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
	owner := op.session.settings
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.ended {
		return Result{}, errors.New("stuntgres: settings attachment ended")
	}
	if err = op.ctx.Err(); err != nil {
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.Set:
		if s.Default {
			if err = op.ctx.Err(); err != nil {
				return Result{}, err
			}
			delete(owner.values, d.Name)
		} else {
			value, err := d.Normalize(s.Values[0])
			if err != nil {
				return Result{}, err
			}
			if err = op.ctx.Err(); err != nil {
				return Result{}, err
			}
			owner.values[d.Name] = value
		}
		return Result{}, nil
	case *parser.Show:
		value, ok := owner.values[d.Name]
		if !ok {
			value = owner.defaults[d.Name]
		}
		return Result{Columns: []parser.Column{d.Column}, Rows: &Snapshot{rows: [][]driver.Value{{value}}, projection: []int{0}}}, nil
	}
	return Result{}, errors.New("stuntgres: invalid setting statement")
}
