package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

func TestPGFileSettingsDefaults(t *testing.T) {
	e := engine.New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"application_name": "file_default_first"}, calls: map[string]int{}}
	a := enginetest.StartSession(t, e, provider)
	provider.values["application_name"] = "file_default_second"
	b := enginetest.StartSession(t, e, provider)
	calls := provider.calls["application_name"]
	if calls != 2 {
		t.Fatal("real initialization provider calls", calls)
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.FileSettingsOID)
	if !ok {
		t.Fatal("missing producer")
	}
	capture := func() {
		t.Helper()
		view := e.CatalogRead()
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.FileSettingsOID}
		in := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: view.MustRelation(address), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
		source, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || source == nil || source.Anonymous == nil || len(source.Anonymous) != 0 {
			t.Fatal("defaults are not file facts", source, err)
		}
		if provider.calls["application_name"] != calls {
			t.Fatal("file source consulted default provider")
		}
	}
	for i, s := range []*session.Session{a, b} {
		want := []string{"file_default_first", "file_default_second"}[i]
		if s.Settings.Defaults["application_name"] != want {
			t.Fatal("captured reset default", s.Settings.Defaults)
		}
		localOwnerValue(t, s, "application_name", want)
		if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SHOW application_name")); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
			t.Fatal("SHOW did not expose real provider default", got, want)
		}
		enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_catalog.pg_file_settings", 0)
		capture()
		enginetest.SessionSQL(t, s, "SET application_name='not_a_file'")
		localOwnerValue(t, s, "application_name", "not_a_file")
		capture()
		enginetest.SessionSQL(t, s, "RESET application_name")
		localOwnerValue(t, s, "application_name", want)
		enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_catalog.pg_file_settings", 0)
		capture()
	}
}
