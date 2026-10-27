package core_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

// TestPublishedCatalogTypeProviderScope checks which descriptors publication
// validates for current type generations. A stage that replaces an existing
// type is checked against every descriptor, so an unchanged user of the old
// generation is rejected; a stage that adds or changes other descriptors is
// checked on its staged descriptors only.
func TestPublishedCatalogTypeProviderScope(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TYPE pair AS (a INT, b INT)")
	enginetest.Run(t, e, "CREATE TABLE holder(p pair)")
	read := e.CatalogRead()
	current := read.State()
	holder := read.MustRelation(enginetest.RoleTableRef(t, e, "holder").Address)
	pair := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(holder.Columns[0].Usage.Type.ID())})
	// A distinct generation of pair: same identity, another descriptor.
	generation := *pair.SQLType
	replaced := *pair
	replaced.SQLType = &generation
	validate := func(staged ...catalog.Descriptor) error {
		t.Helper()
		stage := e.BeginCatalogStage(read)
		for _, d := range staged {
			stage.UpdateDescriptor(d)
		}
		return core.ValidatePublishedCatalog(e, current, stage.ApplyTo(current), stage)
	}
	conflict := func(err error) bool { return errors.As(err, &catalog.Conflict{}) }

	if err := validate(&replaced); !conflict(err) {
		t.Fatal("replaced type left an unchanged user on its old generation", err)
	}
	stale := *holder
	stale.Columns = slices.Clone(holder.Columns)
	stale.Columns[0].Usage.Type = &generation
	if err := validate(&stale); !conflict(err) {
		t.Fatal("staged relation names a type generation the catalog does not hold", err)
	}
	same := *holder
	if err := validate(&same); err != nil {
		t.Fatal(err)
	}
}
