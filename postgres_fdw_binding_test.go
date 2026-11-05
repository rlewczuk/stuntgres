package stuntgres

import (
	"context"
	"errors"
	"testing"
)

// Negative-only registration probe: binding/connector construction must never
// invoke a transport. Genuine successful ownership belongs to reference tests.
type postgresBindingProbe struct{ opens int }

func (p *postgresBindingProbe) Open(context.Context, ExternalSQLConnectRequest) (ExternalSQLConnection, error) {
	p.opens++
	return nil, errors.New("binding unit has no transport")
}
func TestPostgresFDWBinding(t *testing.T) {
	probe := &postgresBindingProbe{}
	d := NewDriver()
	for _, name := range []string{"", "absent"} {
		if err := d.BindPostgresFDWExternalSQLProvider(name); err == nil {
			t.Fatal("unregistered binding accepted", name)
		}
	}
	var typedNil *postgresBindingProbe
	if err := d.RegisterExternalSQLProvider(ExternalSQLProviderDefinition{Name: "nil", Version: "1", Provider: typedNil}); err == nil {
		t.Fatal("typed nil registration accepted")
	}
	definition := ExternalSQLProviderDefinition{Name: "postgres-test-host", Version: "host-version", Provider: probe}
	if err := d.RegisterExternalSQLProvider(definition); err != nil {
		t.Fatal(err)
	}
	if err := d.BindPostgresFDWExternalSQLProvider(definition.Name); err != nil {
		t.Fatal(err)
	}
	if err := d.BindPostgresFDWExternalSQLProvider(definition.Name); err == nil {
		t.Fatal("second binding accepted")
	}
	isolated := NewDriver()
	if err := isolated.BindPostgresFDWExternalSQLProvider(definition.Name); err == nil {
		t.Fatal("another Driver borrowed binding authority")
	}
	connector, err := d.OpenConnector("opaque postgres_fdw host=not-a-transport")
	if err != nil {
		t.Fatal(err)
	}
	engine := connector.(*Connector).engine
	provider := engine.Foreign.PostgresFDWProvider()
	if provider == nil || provider.Name != definition.Name || provider.Version != definition.Version || provider.Provider != probe {
		t.Fatal("connector lost authentic registration identity")
	}
	bindings, _, _, _, _ := engine.Foreign.Definitions()
	bindings.PostgresFDW = nil
	if engine.Foreign.PostgresFDWProvider() != provider {
		t.Fatal("definition copy exposed mutable registry backing")
	}
	if engine.Foreign.DblinkProvider() != nil {
		t.Fatal("stock binding leaked into dblink")
	}
	if err := d.BindPostgresFDWExternalSQLProvider(definition.Name); err == nil {
		t.Fatal("frozen binding changed")
	}
	if err := d.RegisterExternalSQLProvider(ExternalSQLProviderDefinition{Name: "late", Version: "1", Provider: probe}); err == nil {
		t.Fatal("frozen registry changed")
	}
	if probe.opens != 0 {
		t.Fatal("registration/binding/connector performed physical I/O")
	}
}
