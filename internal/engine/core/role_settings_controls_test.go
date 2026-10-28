package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
)

func TestRoleSettingsControlsAuthority(t *testing.T) {
	e := engine.New()
	store := e.RoleStore
	before := store.Published
	role := before.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	key := access.RoleSettingKey{Role: role}
	settings := before.Settings.WithRow(key, []access.RoleSetting{{Name: "application_name", Value: "private-control-default-sentinel"}, {Name: "search_path", Value: "nonexistent_replay_schema"}, {Name: "role", Value: "nonexistent_replay_role"}})
	databaseKey := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
	settings = settings.WithRow(databaseKey, []access.RoleSetting{{Name: "application_name", Value: "database-control-default-sentinel"}, {Name: "search_path", Value: "nonexistent_database_replay_schema"}, {Name: "role", Value: "nonexistent_database_replay_role"}})
	record := before.WithSettings(settings)
	e.Mu.Lock()
	store.PublishLocked(record)
	e.Mu.Unlock()
	row := settings.Lookup(key)
	databaseRow := settings.Lookup(databaseKey)
	assertAuthority := func() {
		t.Helper()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		if e.RoleStore != store || store.Published != record || store.Published.Settings != settings || &settings.Lookup(key)[0] != &row[0] {
			t.Fatal("native control replaced destination role/settings authority or backing")
		}
		if &settings.Lookup(databaseKey)[0] != &databaseRow[0] || databaseRow[0].Value != "database-control-default-sentinel" {
			t.Fatal("native control changed database-only default backing")
		}
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "private-control-default-sentinel") || strings.Contains(dump.String(), "database-control-default-sentinel") || strings.Contains(dump.String(), "nonexistent_replay_role") || strings.Contains(dump.String(), "nonexistent_database_replay_role") {
		t.Fatal("logical dump imported cluster defaults")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	assertAuthority()
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("private replay consumed destination defaults", err)
	}
	assertAuthority()
	if err := engine.Restore(context.Background(), e, strings.NewReader("this is not SQL")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	assertAuthority()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Restore(ctx, e, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled restore", err)
	}
	assertAuthority()
}
