package session

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

func TestSettingsResetAllLocked(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	s := &SettingsStore{
		Values: map[string]string{"application_name": "app", "server_encoding": "UTF8", "x.y": "1"},
		Typed:  map[string]settings.Value{"application_name": {Text: "app"}, "server_encoding": {Text: "UTF8"}, "x.y": {Text: "1"}},
	}
	s.ResetAllLocked(registry)
	if _, ok := s.Values["application_name"]; ok {
		t.Fatal("session setting not reset")
	}
	if _, ok := s.Values["x.y"]; ok {
		t.Fatal("placeholder override not reset")
	}
	if s.Values["server_encoding"] != "UTF8" || s.Typed["server_encoding"].Text != "UTF8" {
		t.Fatal("non-session setting reset")
	}
	if len(s.Actions) != 2 || s.Actions["application_name"].kind != settingSession || !s.Actions["x.y"].original.Present {
		t.Fatal("reset not journaled as session actions", s.Actions)
	}
}

func TestSettingsShowAllRowsLocked(t *testing.T) {
	var typ *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			typ = d
		}
	}
	registry := settings.NewRegistry(nil, false)
	s := &SettingsStore{Values: map[string]string{"application_name": "app"}, Defaults: map[string]string{}}
	rows, err := s.ShowAllRowsLocked(context.Background(), registry, typ)
	if err != nil || len(rows) != len(registry.Order()) {
		t.Fatal(len(rows), err)
	}
	for i, key := range registry.Order() {
		if name, _ := rows[i][0].Text(); name != settings.DisplayName(key) {
			t.Fatalf("row %d name %q, want %q", i, name, settings.DisplayName(key))
		}
		if key == "application_name" {
			if value, _ := rows[i][1].Text(); value != "app" {
				t.Fatalf("application_name shows %q", value)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := s.ShowAllRowsLocked(ctx, registry, typ); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("canceled SHOW ALL returned rows", len(rows), err)
	}
}
