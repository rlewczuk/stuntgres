package reference_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestExternalSQLProviderRegistration(t *testing.T) {
	for _, request := range []stuntgres.ExternalSQLConnectRequest{
		{ConninfoPresent: true, Conninfo: "password=external-secret-conninfo"},
		{MappingOptions: []stuntgres.ForeignOption{{Name: "password", Value: "external-secret-mapping"}}},
	} {
		if rendered := fmt.Sprintf("%v %+v %#v", request, request, request); strings.Contains(rendered, "external-secret") {
			t.Fatal("connection request formatting exposed credentials")
		}
	}
	provider := &fixture.ExternalSQLProvider{}
	for _, name := range []string{"", "unregistered"} {
		if err := stuntgres.NewDriver().BindDblinkExternalSQLProvider(name); err == nil {
			t.Fatal("unregistered direct dblink capability accepted")
		} else if _, sql := err.(interface{ SQLState() string }); sql {
			t.Fatal("binding error invented SQLSTATE", err)
		}
	}
	var typedNil *fixture.ExternalSQLProvider
	for _, def := range []stuntgres.ExternalSQLProviderDefinition{
		{Name: "", Version: "1", Provider: provider},
		{Name: strings.Repeat("x", 64), Version: "1", Provider: provider},
		{Name: "bad\x00name", Version: "1", Provider: provider},
		{Name: "invalid-utf8-\xff", Version: "1", Provider: provider},
		{Name: "valid", Version: "", Provider: provider},
		{Name: "valid", Version: "1"},
		{Name: "valid", Version: "1", Provider: typedNil},
	} {
		if err := stuntgres.NewDriver().RegisterExternalSQLProvider(def); err == nil {
			t.Fatal("accepted invalid external registration")
		}
	}
	d := stuntgres.NewDriver()
	handler := &externalSQLHandler{}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "external_sql_fixture_handler"}, ExternalProvider: "external_sql_fixture", Wrapper: handler}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.OpenConnector("host=opaque port=not-a-port password=not-conninfo"); err == nil {
		t.Fatal("missing executable capability succeeded")
	} else {
		var unavailable *stuntgres.ExternalSQLUnavailableError
		if !errors.As(err, &unavailable) || unavailable.Capability != "external_sql_fixture" {
			t.Fatal("unavailable capability", err)
		}
		if _, ok := err.(interface{ SQLState() string }); ok {
			t.Fatal("fabricated PostgreSQL SQLSTATE", err)
		}
	}
	def := stuntgres.ExternalSQLProviderDefinition{Name: "external_sql_fixture", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}
	if err := d.RegisterExternalSQLProvider(def); err != nil {
		t.Fatal("failed initialization froze registration", err)
	}
	if err := d.RegisterExternalSQLProvider(def); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if err := d.BindDblinkExternalSQLProvider(def.Name); err != nil {
		t.Fatal(err)
	}
	if err := d.BindDblinkExternalSQLProvider(def.Name); err == nil {
		t.Fatal("second dblink binding accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixture.ScenarioBudget)
	defer cancel()
	keys := []string{"host=opaque port=not-a-port password=not-conninfo", " host=opaque port=not-a-port password=not-conninfo", ""}
	for i, key := range keys {
		connector, err := d.OpenConnector(key)
		if err != nil {
			t.Fatal("Driver key interpreted as remote conninfo", err)
		}
		db, recorder := fixture.RecordedNativePool(t, connector)
		fixture.RunStep(t, ctx, db, recorder, fixture.Exec("CREATE TABLE external_sql_isolation(v integer)", 0))
		fixture.RunStep(t, ctx, db, recorder, fixture.Exec("INSERT INTO external_sql_isolation VALUES($1)", 1, int64(i+1)))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT v FROM external_sql_isolation", []string{"v"}, []string{"INT4"}, [][]any{{int64(i + 1)}})
			s.Prepared = prepared
			fixture.RunStep(t, ctx, db, recorder, s)
		}
	}
	same, err := d.OpenConnector(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	reopened, recorder := fixture.RecordedNativePool(t, same)
	fixture.RunStep(t, ctx, reopened, recorder, fixture.Select("SELECT v FROM external_sql_isolation", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "late", Version: "1", Provider: provider}); err == nil {
		t.Fatal("registry not frozen")
	}
	if err := d.BindDblinkExternalSQLProvider(def.Name); err == nil {
		t.Fatal("dblink binding registry not frozen")
	}
	independent := stuntgres.NewDriver()
	if err := independent.RegisterExternalSQLProvider(def); err != nil {
		t.Fatal("independent Driver inherited registration/freeze", err)
	}
	isolated, err := independent.OpenConnector(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	other, otherRecorder := fixture.RecordedNativePool(t, isolated)
	fixture.RunStep(t, ctx, other, otherRecorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='external_sql_isolation'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if len(provider.Snapshot()) != 0 {
		t.Fatal("registration/connector opened a physical remote session")
	}
}
