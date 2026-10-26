package session

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// TestSettingAccessAllowedCallback pins the access classes and the
// parameter-grant callback: eligible privileged builtins consult it, its
// error and verdict pass through, and other classes keep their errors.
func TestSettingAccessAllowedCallback(t *testing.T) {
	refuse := errors.New("authority failed")
	cases := []struct {
		name     string
		d        settings.Definition
		allowed  bool
		failure  error
		consults bool
		code     string
		message  string
	}{
		{name: "user", d: settings.Definition{Name: "application_name", Access: settings.Session}},
		{name: "startup", d: settings.Definition{Name: "port", Access: settings.StartupOnly}, code: "55P02", message: `parameter "port" cannot be changed without restarting the server`},
		{name: "sighup", d: settings.Definition{Name: "allow_alter_system", Access: settings.Sighup}, allowed: true, code: "55P02", message: `parameter "allow_alter_system" cannot be changed now`},
		{name: "large object granted", d: settings.Definition{Name: "lo_compat_privileges", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "large object denied", d: settings.Definition{Name: "lo_compat_privileges", Access: settings.Privileged}, consults: true, code: "42501", message: `permission denied to set parameter "lo_compat_privileges"`},
		{name: "large object failure", d: settings.Definition{Name: "lo_compat_privileges", Access: settings.Privileged}, failure: refuse, consults: true},
		{name: "read-only", d: settings.Definition{Name: "server_version", Access: settings.ReadOnly}, code: "55P02", message: `parameter "server_version" cannot be changed`},
		{name: "statement stats granted", d: settings.Definition{Name: "log_statement_stats", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "statement stats denied", d: settings.Definition{Name: "log_statement_stats", Access: settings.Privileged}, consults: true, code: "42501", message: `permission denied to set parameter "log_statement_stats"`},
		{name: "parser stats granted", d: settings.Definition{Name: "log_parser_stats", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "planner stats granted", d: settings.Definition{Name: "log_planner_stats", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "executor stats granted", d: settings.Definition{Name: "log_executor_stats", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "logging granted", d: settings.Definition{Name: "log_min_messages", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "logging denied", d: settings.Definition{Name: "log_min_messages", Access: settings.Privileged}, consults: true, code: "42501", message: `permission denied to set parameter "log_min_messages"`},
		{name: "logging failure", d: settings.Definition{Name: "log_min_messages", Access: settings.Privileged}, failure: refuse, consults: true},
		{name: "lc_messages granted", d: settings.Definition{Name: "lc_messages", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "lc_messages denied", d: settings.Definition{Name: "lc_messages", Access: settings.Privileged}, consults: true, code: "42501", message: `permission denied to set parameter "lc_messages"`},
		{name: "lc_messages failure", d: settings.Definition{Name: "lc_messages", Access: settings.Privileged}, failure: refuse, consults: true},
		{name: "dynamic path granted", d: settings.Definition{Name: "dynamic_library_path", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "event trigger granted", d: settings.Definition{Name: "event_triggers", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "extension path granted", d: settings.Definition{Name: "extension_control_path", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "session preload granted", d: settings.Definition{Name: "session_preload_libraries", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "replication role denied", d: settings.Definition{Name: "session_replication_role", Access: settings.Privileged}, consults: true, code: "42501", message: `permission denied to set parameter "session_replication_role"`},
		{name: "custom deny-only", d: settings.Definition{Name: "custom.privileged", Access: settings.Privileged}, allowed: true, code: "42501", message: `permission denied to set parameter "custom.privileged"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			consulted := ""
			err := SettingAccess(c.d, func(name string) (bool, error) {
				consulted = name
				return c.allowed, c.failure
			})
			if c.consults != (consulted == c.d.Name) {
				t.Fatalf("callback consulted %q", consulted)
			}
			switch {
			case c.failure != nil:
				if err != c.failure {
					t.Fatalf("error %v, want %v", err, c.failure)
				}
			case c.code == "":
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
			default:
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != c.code || diagnostic.Message != c.message {
					t.Fatalf("error %v, want %s %s", err, c.code, c.message)
				}
			}
		})
	}
}

func TestClientPrivilegedResetAll(t *testing.T) {
	store := &SettingsStore{Values: make(map[string]string), Typed: make(map[string]settings.Value)}
	for _, name := range []string{"lc_messages", "dynamic_library_path", "event_triggers", "extension_control_path", "session_preload_libraries", "session_replication_role", "lo_compat_privileges", "custom.privileged", "log_statement_stats", "log_parser_stats", "log_planner_stats", "log_executor_stats"} {
		store.Values[name] = "changed"
	}
	custom, err := settings.Validate(settings.Definition{Name: "custom.privileged", Type: settings.String, Access: settings.Privileged})
	if err != nil {
		t.Fatal(err)
	}
	registry := settings.NewRegistry([]settings.Definition{custom}, false)
	store.ResetAllLocked(registry)
	if len(store.Values) != 1 || store.Values["custom.privileged"] != "changed" {
		t.Fatal("RESET ALL changed deny-only values or retained eligible builtin", store.Values)
	}
}
