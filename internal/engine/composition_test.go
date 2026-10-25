package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

type initializationTestProvider struct{ validated int }

func (*initializationTestProvider) ClassID() ids.OID { return 90000 }

func (p *initializationTestProvider) Validate(_ catalog.Read, d catalog.Descriptor) error {
	if d.Address().ClassID != p.ClassID() {
		return errors.New("unexpected descriptor class")
	}
	p.validated++
	return nil
}

func TestCatalogProviderRegistersBeforeInitialization(t *testing.T) {
	provider := &initializationTestProvider{}
	e, err := NewWithCatalogProviders(provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider.validated != 0 {
		t.Fatalf("custom provider received unexpected builtin callbacks: %d", provider.validated)
	}

	owner, err := e.ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	descriptor := &catalog.Namespace{
		ObjectAddress:  ids.ObjectAddress{ClassID: provider.ClassID(), ObjectID: 1},
		ObjectName:     "feature",
		ObjectRevision: 1,
		Owner:          owner,
	}
	if err := stage.Create(descriptor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatalf("registered provider could not validate its descriptor: %v", err)
	}
	if provider.validated != 1 {
		t.Fatalf("provider validations = %d, want 1", provider.validated)
	}
	foreignOwner, err := New().ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidOwner := range []authorization.PrincipalRef{{}, foreignOwner} {
		invalid := *descriptor
		invalid.Owner = invalidOwner
		invalidStage := e.BeginCatalogStage(e.CatalogRead())
		if err := invalidStage.Create(&invalid); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), invalidStage); err == nil {
			t.Fatal("provider descriptor accepted zero or foreign owner")
		}
		e.DiscardCatalogStage(invalidStage)
	}
	if err := e.RegisterCatalogProvider(&initializationTestProvider{}); err == nil {
		t.Fatal("provider registered after initialization")
	}

	var uninitialized core.Engine
	if err := uninitialized.RegisterCatalogProvider(&initializationTestProvider{}); err == nil {
		t.Fatal("zero-value Engine accepted registration")
	}
}
