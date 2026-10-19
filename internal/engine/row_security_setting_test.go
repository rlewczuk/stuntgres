package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestRowSecuritySettingsCapture(t *testing.T) {
	e := New()
	settings := &sessionSettings{defaults: make(map[string]string), values: make(map[string]string)}
	if err := settings.initialize(context.Background(), e.settingsDefinitions, e.settingsOrder, nil); err != nil {
		t.Fatal(err)
	}
	session := &Session{settings: settings}
	op := &Operation{session: session}
	first := newStatementScope(op, &transactionOwner{}, time.Time{})
	settings.mu.Lock()
	settings.values["row_security"] = "off"
	settings.mu.Unlock()
	second := newStatementScope(op, &transactionOwner{}, time.Time{})
	if !first.rowSecurity || second.rowSecurity {
		t.Fatal("statement did not retain/recapture setting", first.rowSecurity, second.rowSecurity)
	}
	settings.mu.Lock()
	delete(settings.values, "row_security")
	settings.mu.Unlock()
	third := newStatementScope(op, &transactionOwner{}, time.Time{})
	if !third.rowSecurity || second.rowSecurity {
		t.Fatal("restored default changed prior scope")
	}
}

func TestRowSecuritySettingProvenance(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"row_security": "off"}, calls: map[string]int{}}
	settings := &sessionSettings{defaults: make(map[string]string), values: make(map[string]string)}
	if err := settings.initialize(context.Background(), e.settingsDefinitions, e.settingsOrder, provider); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgSettingsOID})
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	capture := func(value, source string) *querySource {
		t.Helper()
		rows, err := capturePGSettings(context.Background(), ref, d, settings)
		if err != nil || len(rows.anonymous) != 1 {
			t.Fatal(rows, err)
		}
		for index, want := range map[int]string{1: value, 8: source, 12: "on", 13: "off"} {
			got, err := rows.anonymous[0][index].Text()
			if err != nil || got != want {
				t.Fatal(index, got, want, err)
			}
		}
		if !rows.anonymous[0][14].IsNull() || !rows.anonymous[0][15].IsNull() {
			t.Fatal("invented configuration-file provenance")
		}
		return rows
	}
	old := capture("off", "client")
	// Provider changes are not live defaults, even when SQL resets an override.
	provider.values["row_security"] = "on"
	settings.values["row_security"] = "on"
	capture("on", "session")
	settings.values["row_security"] = "off"
	capture("off", "session")
	delete(settings.values, "row_security")
	capture("off", "client")
	if provider.calls["row_security"] != 1 {
		t.Fatal(provider.calls)
	}
	if got, _ := old.anonymous[0][8].Text(); got != "client" {
		t.Fatal("retained metadata changed", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := capturePGSettings(ctx, ref, d, settings); source != nil || err != context.Canceled {
		t.Fatal(source, err)
	}
}
