package tablecmds_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

func TestPartitionExclusionAdoptionIdentity(t *testing.T) {
	e := partitionExclusionAdoptionEngine(t)
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	defer enginetest.SessionSQL(t, s, "ROLLBACK")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		t.Helper()
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			t.Fatal(err)
		}
		if err := scope.Finish(fn(scope)); err != nil {
			t.Fatal(err)
		}
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("adoption_select", "SELECT COALESCE(-(7::integer),0),k FROM leaf", nil)
		return err
	})
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "adoption_select", &storageBindInput{bytes.NewReader([]byte{0, 0, 0, 0, 0, 0})})
			return err
		})
	}
	bind("adoption_before")
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("adoption_select")
	owner := definition.Description.Folds
	portal := registry.Binding("adoption_before")
	if owner == nil || portal == nil || portal.Folds == nil || portal.Resource == nil || portal.Resource.Root == nil {
		t.Fatal("missing accepted portal owner/graph")
	}
	accepted := *portal.Folds
	analysis, graph := portal.Entry.Description.Analysis, portal.Resource.Root
	if analysis == nil || owner.Analysis != analysis || portal.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || accepted.Generation == 0 || analysis.Domain != e.Catalog.Domain || analysis.Epoch != e.CatalogRead().Epoch() {
		t.Fatal("missing accepted definition authority")
	}
	entries := append([]exec.FoldConstant(nil), accepted.Entries...)
	dependencies := append([]catalog.ExpressionDependency(nil), analysis.Dependencies...)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	leaf := before.MustRelation(roleTableRef(t, e, "leaf").Address)
	child := before.MustRelation(roleTableRef(t, e, "child_idx").Address)
	parent := before.MustRelation(roleTableRef(t, e, "parent_idx").Address)
	old := leaf.Constraints.Exclusion[0]
	store, backing := e.Storage.Relations[leaf.Address()], e.Storage.IndexRoots[child.Address()]
	if backing == nil || len(backing.Entries) != 1 {
		t.Fatal("fixture lacks actual populated backing")
	}
	found := false
	for _, dependency := range analysis.Dependencies {
		if dependency.Ref.Address == leaf.Address() {
			found = dependency.Revision == leaf.Revision() && dependency.Ref.Domain() == analysis.Domain
		}
	}
	if !found {
		t.Fatal("accepted analysis lacks actual table revision")
	}
	stage := partitionConstraintAttachStage(t, e, op)
	candidate := e.StageRead(stage)
	adopted := candidate.MustRelation(leaf.Address()).Constraints.Exclusion[0]
	if adopted.OID != old.OID || adopted.Index != old.Index || adopted.Name != old.Name || adopted.Parent != parent.SQLIndex.Constraint.OID || adopted.IsLocal || !adopted.NoInherit || &adopted.Operators[0] != &old.Operators[0] {
		t.Fatal("adoption replaced identity, owned operators or provenance", adopted, old)
	}
	if leaf.Constraints.Exclusion[0].Parent != 0 || !leaf.Constraints.Exclusion[0].IsLocal || child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || e.Catalog.State != before.State() {
		t.Fatal("staging mutated live predecessor")
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	live := e.CatalogRead().MustRelation(child.Address())
	if live.SQLIndex.PartitionParent != parent.Address() || live.SQLIndex.Constraint.OID != old.OID || e.Storage.Relations[leaf.Address()] != store || e.Storage.IndexRoots[child.Address()] != backing || len(backing.Entries) != 1 {
		t.Fatal("metadata adoption rebuilt physical authority")
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), live); err != nil {
		t.Fatal(err)
	}
	bind("adoption_after")
	next := registry.Binding("adoption_after")
	if next == nil || next == portal || next.Resource.Root == graph || next.Entry.Description.Analysis == analysis || next.Entry.Description.Folds != owner || definition.Description.Folds != owner || owner.Analysis != next.Entry.Description.Analysis {
		t.Fatal("actual owner failed to refresh changed relation analysis")
	}
	// The definition's current reuse generation may advance after the real
	// relation revision changes. The accepted portal owns its prior graph/group;
	// neither successful adoption nor successor Bind may mutate that backing.
	if portal.Resource.Root != graph || portal.Entry.Description.Analysis != analysis || portal.Folds.Generation != accepted.Generation || &portal.Folds.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(portal.Folds.Entries, entries) || !reflect.DeepEqual(analysis.Dependencies, dependencies) || owner.Current().Generation < accepted.Generation {
		t.Fatal("adoption mutated accepted portal backing/generation/analysis")
	}
	partitionExclusionAdoptionIneligible(t)
}
