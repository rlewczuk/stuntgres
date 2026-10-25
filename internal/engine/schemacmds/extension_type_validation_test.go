package schemacmds_test

import (
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

type stubTypeProvider struct{ err error }

func (stubTypeProvider) ClassID() ids.OID { return ids.TypeClassID }

func (p stubTypeProvider) Validate(catalog.Read, catalog.Descriptor) error { return p.err }

// Installed codec types validate their associated array through the type
// provider registered in the catalog owner, not a fixed engine provider.
func TestInstalledCodecArrayUsesRegisteredTypeProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		install  func(*testing.T) (*core.Engine, *catalog.Extension, *catalog.Type)
		provider catalog.ExtensionImplementation
	}{
		{"intarray", intarrayUnitInstalled, extension.StockIntarray},
		{"cube", cubeUnitInstalled, extension.StockCube},
		{"citext", citextUnitInstalled, extension.StockCitext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, ext, _ := tc.install(t)
			view := e.CatalogRead()
			if err := tc.provider.ValidateExtension(view, ext); err != nil {
				t.Fatal(err)
			}
			owner := *view.Owner()
			owner.Providers = maps.Clone(owner.Providers)
			sentinel := errors.New("registered type provider")
			owner.Providers[ids.TypeClassID] = stubTypeProvider{sentinel}
			if err := tc.provider.ValidateExtension(catalog.NewRead(&owner, e.Catalog.State), ext); !errors.Is(err, sentinel) {
				t.Fatalf("stubbed type provider: got %v", err)
			}
			delete(owner.Providers, ids.TypeClassID)
			if err := tc.provider.ValidateExtension(catalog.NewRead(&owner, e.Catalog.State), ext); err == nil {
				t.Fatal("missing type provider accepted")
			}
		})
	}
}
