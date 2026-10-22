package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	settingspkg "stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

func TestRowSecuritySettingsCapture(t *testing.T) {
	e := New()
	settings := &session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := settings.Initialize(context.Background(), e.settings.Definitions(), e.settings.Order(), nil); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Settings: settings}
	op := &session.Operation{Session: sess}
	first := newStatementScope(op, &session.TransactionOwner{}, time.Time{})
	settings.Mu.Lock()
	settings.Values["row_security"] = "off"
	settings.Mu.Unlock()
	second := newStatementScope(op, &session.TransactionOwner{}, time.Time{})
	if !first.rowSecurity || second.rowSecurity {
		t.Fatal("statement did not retain/recapture setting", first.rowSecurity, second.rowSecurity)
	}
	settings.Mu.Lock()
	delete(settings.Values, "row_security")
	settings.Mu.Unlock()
	third := newStatementScope(op, &session.TransactionOwner{}, time.Time{})
	if !third.rowSecurity || second.rowSecurity {
		t.Fatal("restored default changed prior scope")
	}
}

func TestRowSecuritySettingProvenance(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"row_security": "off"}, calls: map[string]int{}}
	settings := &session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := settings.Initialize(context.Background(), e.settings.Definitions(), e.settings.Order(), provider); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SettingsOID})
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	capture := func(value, source string) *exec.Source {
		t.Helper()
		rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{settings: settings, registry: e.settings}}, capturePGSettings)
		if err != nil || len(rows.Anonymous) != len(e.settings.Order()) {
			t.Fatal(rows, err)
		}
		row := settingsMetadataRow(t, rows.Anonymous, "row_security")
		for index, want := range map[int]string{1: value, 8: source, 12: "on", 13: "off"} {
			got, err := row[index].Text()
			if err != nil || got != want {
				t.Fatal(index, got, want, err)
			}
		}
		if !row[14].IsNull() || !row[15].IsNull() {
			t.Fatal("invented configuration-file provenance")
		}
		return rows
	}
	old := capture("off", "client")
	// Provider changes are not live defaults, even when SQL resets an override.
	provider.values["row_security"] = "on"
	settings.Values["row_security"] = "on"
	settings.Typed["row_security"] = settingspkg.Value{Boolean: true}
	capture("on", "session")
	settings.Values["row_security"] = "off"
	settings.Typed["row_security"] = settingspkg.Value{Boolean: false}
	capture("off", "session")
	delete(settings.Values, "row_security")
	delete(settings.Typed, "row_security")
	capture("off", "client")
	if provider.calls["row_security"] != 1 {
		t.Fatal(provider.calls)
	}
	if got, _ := settingsMetadataRow(t, old.Anonymous, "row_security")[8].Text(); got != "client" {
		t.Fatal("retained metadata changed", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Ref: ref, Source: d, Runtime: engineCatalogRuntime{settings: settings, registry: e.settings}}, capturePGSettings); source != nil || err != context.Canceled {
		t.Fatal(source, err)
	}
}

func settingsMetadataRow(t *testing.T, rows [][]sqltype.Value, name string) []sqltype.Value {
	t.Helper()
	for _, row := range rows {
		if got, _ := row[0].Text(); got == name {
			return row
		}
	}
	t.Fatalf("missing setting %s", name)
	return nil
}
