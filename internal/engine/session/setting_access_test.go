package session

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// TestSettingAccessAllowedCallback pins the access classes and the
// parameter-grant callback: only privileged lc_messages consults it, its
// error and its verdict pass through, and other classes keep their errors.
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
		{name: "read-only", d: settings.Definition{Name: "server_version", Access: settings.ReadOnly}, code: "55P02", message: `parameter "server_version" cannot be changed`},
		{name: "privileged other", d: settings.Definition{Name: "log_min_messages", Access: settings.Privileged}, code: "42501", message: `permission denied to set parameter "log_min_messages"`},
		{name: "lc_messages granted", d: settings.Definition{Name: "lc_messages", Access: settings.Privileged}, allowed: true, consults: true},
		{name: "lc_messages denied", d: settings.Definition{Name: "lc_messages", Access: settings.Privileged}, consults: true, code: "42501", message: `permission denied to set parameter "lc_messages"`},
		{name: "lc_messages failure", d: settings.Definition{Name: "lc_messages", Access: settings.Privileged}, failure: refuse, consults: true},
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
