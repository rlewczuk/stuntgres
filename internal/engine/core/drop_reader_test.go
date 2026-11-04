package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// TestDropReaderSeam pins the view.DropReader seam on dropProvider: a staged
// drop reads SQL dependencies through its stage, a prepared drop through its
// preparation, and a bare provider has no definition input. Path is the
// stage's dependency path, and RetirementDescriptor still reads a removed
// relation.
func TestDropReaderSeam(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE retiring(id INTEGER)")
	read := e.CatalogRead()
	relation := read.MustRelation(roleTableRef(t, e, "retiring").Address)

	var bare view.DropReader = core.StageDropProvider(context.Background(), nil, nil, nil, nil)
	if input := bare.DefinitionInput(); input != nil {
		t.Fatalf("bare DefinitionInput = %T, want nil", input)
	}

	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	stage.DependencyPath = path
	var staged view.DropReader = core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	if input, ok := staged.DefinitionInput().(core.StageDefinitionReader); !ok || core.StageDefinitionReaderStage(&input) != stage {
		t.Fatalf("staged DefinitionInput = %T, want the stage reader", staged.DefinitionInput())
	}
	if !slices.Equal(staged.Path(), path) {
		t.Fatalf("Path = %v, want %v", staged.Path(), path)
	}
	if err := stage.Remove(e.Catalog.Domain.Ref(read.Epoch(), relation.Address())); err != nil {
		t.Fatal(err)
	}
	if !staged.Removed(relation.Address()) || staged.RetirementDescriptor(relation.Address()) != relation {
		t.Fatal("RetirementDescriptor must still read the removed relation")
	}

	prepared := &table.DropPreparation{Base: read, Discovery: read}
	var preparedReader view.DropReader = &core.DropProvider{Base: read, Prepared: prepared}
	if input, ok := preparedReader.DefinitionInput().(*table.DropPreparation); !ok || input != prepared {
		t.Fatalf("prepared DefinitionInput = %T, want the preparation", preparedReader.DefinitionInput())
	}
}

// Preparation owns exact effects, not a pretend already-removed catalog.
// A secondary sequence phase must neither repeat a primary view effect nor
// discard a genuinely new external consumer of the sequence itself.
func TestDropCapturedOwnedPhases(t *testing.T) {
	for _, tc := range []struct {
		name, dependent string
		mode            parser.DropMode
	}{
		{name: "own_check_and_default", mode: parser.DropRestrict},
		{name: "primary_once", dependent: "SELECT id FROM retiring_owned", mode: parser.DropCascade},
		{name: "secondary_restrict", dependent: "SELECT last_value FROM retiring_owned_id_seq", mode: parser.DropRestrict},
		{name: "secondary_cascade", dependent: "SELECT last_value FROM retiring_owned_id_seq", mode: parser.DropCascade},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE TABLE retiring_owned(id serial,CHECK(nextval('retiring_owned_id_seq')>0))")
			if tc.dependent != "" {
				enginetest.RoleExecute(t, e, op, "CREATE VIEW retiring_dependent AS "+tc.dependent)
			}
			base := e.CatalogRead()
			root := roleTableRef(t, e, "retiring_owned")
			sequence := roleTableRef(t, e, "retiring_owned_id_seq")
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			e.Mu.Lock()
			err := stage.SelectDropRoot(root, false)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			drop := core.StageDropProvider(op.Context(), stage, nil, nil, nil)
			plan, err := drop.PrepareExpressionDependencies([]ids.ObjectAddress{root.Address}, tc.mode, ids.ObjectAddress{})
			if stage.RemovalCount() != 0 || stage.StageRead().Get(root.Address) != base.Get(root.Address) || e.CatalogRead().State() != base.State() {
				t.Fatal("preparation changed actual catalog liveness")
			}
			if tc.name == "secondary_restrict" {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop table retiring_owned because other objects depend on it", Detail: "view retiring_dependent depends on sequence retiring_owned_id_seq", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatal("new secondary consumer lost its original RESTRICT subject/detail", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = stage.ApplySelectedDropRoots(plan); err != nil {
				t.Fatal(err)
			}
			if err = drop.ApplyExpressionDependencies(plan); err != nil {
				t.Fatal(err)
			}
			if err = drop.ApplyOwnedExpressionDependencies(plan); err != nil {
				t.Fatal(err)
			}
			if !stage.Removed(root.Address) || !stage.Removed(sequence.Address) {
				t.Fatal("actual table/owned-sequence cohort was not retired")
			}
			var notices []string
			if tc.dependent != "" {
				dependent := roleTableRef(t, e, "retiring_dependent")
				if !stage.Removed(dependent.Address) {
					t.Fatal("captured external consumer survived")
				}
				notices = []string{"drop cascades to view retiring_dependent"}
			}
			if !slices.Equal(stage.DropCascadeNames, notices) {
				t.Fatal("primary/secondary phases duplicated or lost notices", stage.DropCascadeNames, notices)
			}
			if err := drop.ApplyOwnedExpressionDependencies(plan); err != ids.ErrCatalogReplaced {
				t.Fatal("owned phase was not consumed exactly once", err)
			}
			if e.CatalogRead().State() != base.State() || base.Get(root.Address) == nil || base.Get(sequence.Address) == nil {
				t.Fatal("private retirement changed the published predecessor")
			}
		})
	}
}
