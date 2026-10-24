package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

// TestDropReaderSeam pins the view.DropReader seam on dropProvider: a staged
// drop reads SQL dependencies through its stage, a prepared drop through its
// preparation, and a bare provider has no definition input. Path is the
// stage's dependency path, and RetirementDescriptor still reads a removed
// relation.
func TestDropReaderSeam(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE retiring(id INTEGER)")
	read := e.CatalogRead()
	relation := read.MustRelation(roleTableRef(t, e, "retiring").Address)

	var bare view.DropReader = stageDropProvider(context.Background(), nil, nil, nil, nil)
	if input := bare.DefinitionInput(); input != nil {
		t.Fatalf("bare DefinitionInput = %T, want nil", input)
	}

	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	stage.dependencyPath = path
	var staged view.DropReader = stageDropProvider(context.Background(), stage, nil, nil, nil)
	if input, ok := staged.DefinitionInput().(stageDefinitionReader); !ok || input.stage != stage {
		t.Fatalf("staged DefinitionInput = %T, want the stage reader", staged.DefinitionInput())
	}
	if !slices.Equal(staged.Path(), path) {
		t.Fatalf("Path = %v, want %v", staged.Path(), path)
	}
	if err := stage.Remove(e.catalog.Domain.Ref(read.Epoch(), relation.Address())); err != nil {
		t.Fatal(err)
	}
	if !staged.Removed(relation.Address()) || staged.RetirementDescriptor(relation.Address()) != relation {
		t.Fatal("RetirementDescriptor must still read the removed relation")
	}

	prepared := &preparedDrop{Base: read, Discovery: read}
	var preparedReader view.DropReader = &dropProvider{base: read, prepared: prepared}
	if input, ok := preparedReader.DefinitionInput().(*preparedDrop); !ok || input != prepared {
		t.Fatalf("prepared DefinitionInput = %T, want the preparation", preparedReader.DefinitionInput())
	}
}
