package session

import (
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestLoggingSettingsProviderCapture(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	capture := func(p settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: map[string]string{}, Values: map[string]string{}}
		err := s.Initialize(t.Context(), r.Definitions(), r.Order(), p)
		return s, err
	}
	p := &clientDefaultProvider{physicalDefaultProvider: physicalDefaultProvider{"log_connections": "receipt, authentication", "log_disconnections": "true", "application_name": strings.Repeat("x", 64), "log_statement": "ALL"}}
	old, err := capture(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"log_connections": "receipt, authentication", "log_disconnections": "on", "application_name": strings.Repeat("x", 63), "log_statement": "all"}
	for name, value := range want {
		if old.Value(name) != value {
			t.Fatal(name, old.Value(name), value)
		}
	}
	if p.calls != len(r.Order()) {
		t.Fatal("capture count", p.calls)
	}
	p.physicalDefaultProvider["log_connections"] = "all"
	old.ResetAllLocked(r)
	for name, value := range want {
		if old.Value(name) != value {
			t.Fatal("reset capture", name, old.Value(name), value)
		}
	}
	if p.calls != len(r.Order()) {
		t.Fatal("provider reentered", p.calls)
	}
	replacement, err := capture(p)
	if err != nil || replacement.Value("log_connections") != "all" || old.Value("log_connections") != "receipt, authentication" {
		t.Fatal("physical isolation", err)
	}
	for _, tc := range []struct{ name, alias, changed string }{{"logging_collector", "false", "on"}, {"cluster_name", "", "other"}, {"event_source", "PostgreSQL", "other"}, {"log_directory", "./log/", "other"}, {"log_file_mode", "384", "0640"}, {"log_timezone", "gmt", "UTC"}, {"log_rotation_age", "24h", "2h"}, {"log_checkpoints", "true", "off"}} {
		if _, err := capture(physicalDefaultProvider{tc.name: tc.alias}); err != nil {
			t.Fatal("equivalent immutable", tc, err)
		}
		if _, err := capture(physicalDefaultProvider{tc.name: tc.changed}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name {
			t.Fatal("changed immutable", tc, err)
		}
	}
	for name, value := range map[string]string{"log_connections": "receipt, on", "log_disconnections": "broken", "log_timezone": "UTC0:00:30", "log_destination": "eventlog", "log_file_mode": "512"} {
		if _, err := capture(physicalDefaultProvider{name: value}); err == nil {
			t.Fatal("invalid capture", name, value)
		}
	}
}

func TestLoggingSettingsAccessAndReset(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	s := &SettingsStore{Values: map[string]string{}, Typed: map[string]settings.Value{}}
	for _, name := range r.Order() {
		d := r.Definitions()[name]
		if !strings.HasPrefix(d.Category, "Reporting and Logging /") {
			continue
		}
		s.Values[name] = "sentinel"
		calls := 0
		err := SettingAccess(d, func(string) (bool, error) { calls++; return true, nil })
		if d.Access == settings.Session || d.Access == settings.Privileged {
			if err != nil || calls != map[bool]int{true: 1, false: 0}[d.Access == settings.Privileged] {
				t.Fatal("mutable access", name, calls, err)
			}
			if d.Access == settings.Privileged {
				err = SettingAccess(d, func(string) (bool, error) { return false, nil })
				var e *sqlstate.Error
				if !errors.As(err, &e) || e.Code != "42501" {
					t.Fatal(name, err)
				}
			}
		} else {
			var e *sqlstate.Error
			if calls != 0 || !errors.As(err, &e) || e.Code != "55P02" {
				t.Fatal("context before authority", name, calls, err)
			}
		}
	}
	for _, name := range []string{"custom.privileged", "log_statement_stats"} {
		calls := 0
		err := SettingAccess(settings.Definition{Name: name, Access: settings.Privileged}, func(string) (bool, error) { calls++; return true, nil })
		if err == nil || calls != 0 {
			t.Fatal("deny-only", name, calls, err)
		}
	}
	s.ResetAllLocked(r)
	for _, name := range r.Order() {
		d := r.Definitions()[name]
		if !strings.HasPrefix(d.Category, "Reporting and Logging /") {
			continue
		}
		_, present := s.Values[name]
		if present != (d.Access != settings.Session && d.Access != settings.Privileged) {
			t.Fatal("reset eligibility", name, present)
		}
	}
}

func TestLoggingSettingsListFlattening(t *testing.T) {
	for _, name := range []string{"log_connections", "LOG_DESTINATION", "log_statement", "log_timezone", "log_directory"} {
		s := &parser.Set{Name: parser.Identifier{Text: name}, Values: []string{"a", "b"}}
		input, err := SetInput(nil, s, false)
		if name == "log_connections" || name == "LOG_DESTINATION" {
			if err != nil || input == nil || *input != "a, b" {
				t.Fatal(name, input, err)
			}
		} else {
			var e *sqlstate.Error
			if !errors.As(err, &e) || e.Code != "22023" || e.Message != "SET "+name+" takes only one argument" {
				t.Fatal(name, err)
			}
		}
	}
}
