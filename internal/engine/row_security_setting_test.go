package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestRowSecuritySettingsCapture(t *testing.T) {
	e := New()
	settings := &sessionSettings{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := settings.Initialize(context.Background(), e.settings.Definitions(), e.settings.Order(), nil); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Settings: settings}
	op := &Operation{Session: sess}
	first := newStatementScope(op, &transactionOwner{}, time.Time{})
	settings.Mu.Lock()
	settings.Values["row_security"] = "off"
	settings.Mu.Unlock()
	second := newStatementScope(op, &transactionOwner{}, time.Time{})
	if !first.rowSecurity || second.rowSecurity {
		t.Fatal("statement did not retain/recapture setting", first.rowSecurity, second.rowSecurity)
	}
	settings.Mu.Lock()
	delete(settings.Values, "row_security")
	settings.Mu.Unlock()
	third := newStatementScope(op, &transactionOwner{}, time.Time{})
	if !third.rowSecurity || second.rowSecurity {
		t.Fatal("restored default changed prior scope")
	}
}

func TestRowSecuritySettingProvenance(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"row_security": "off"}, calls: map[string]int{}}
	settings := &sessionSettings{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := settings.Initialize(context.Background(), e.settings.Definitions(), e.settings.Order(), provider); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SettingsOID})
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	capture := func(value, source string) *exec.Source {
		t.Helper()
		rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{settings: settings}}, capturePGSettings)
		if err != nil || len(rows.Anonymous) != 1 {
			t.Fatal(rows, err)
		}
		for index, want := range map[int]string{1: value, 8: source, 12: "on", 13: "off"} {
			got, err := rows.Anonymous[0][index].Text()
			if err != nil || got != want {
				t.Fatal(index, got, want, err)
			}
		}
		if !rows.Anonymous[0][14].IsNull() || !rows.Anonymous[0][15].IsNull() {
			t.Fatal("invented configuration-file provenance")
		}
		return rows
	}
	old := capture("off", "client")
	// Provider changes are not live defaults, even when SQL resets an override.
	provider.values["row_security"] = "on"
	settings.Values["row_security"] = "on"
	capture("on", "session")
	settings.Values["row_security"] = "off"
	capture("off", "session")
	delete(settings.Values, "row_security")
	capture("off", "client")
	if provider.calls["row_security"] != 1 {
		t.Fatal(provider.calls)
	}
	if got, _ := old.Anonymous[0][8].Text(); got != "client" {
		t.Fatal("retained metadata changed", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Ref: ref, Source: d, Runtime: engineCatalogRuntime{settings: settings}}, capturePGSettings); source != nil || err != context.Canceled {
		t.Fatal(source, err)
	}
}
