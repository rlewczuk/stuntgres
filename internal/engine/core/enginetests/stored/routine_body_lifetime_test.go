package stored_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestRoutineBodyStoredRecipeReplacement(t *testing.T) {
	ctx := context.Background()
	source := engine.New()
	setup := enginetest.StartSession(t, source, nil)
	enginetest.SessionSQL(t, setup, "CREATE TABLE body_defaults(v integer DEFAULT bit_length('abc'))")
	enginetest.SessionSQL(t, setup, "CREATE VIEW body_view AS SELECT bit_length('abc') AS v")
	type recipe struct {
		consumer *catalog.Relation
		node     expr.Expr
		refs     []ids.ObjectRef
		checks   []catalog.ExpressionDependency
	}
	capture := func(e *core.Engine) []recipe {
		view := e.CatalogRead()
		table := view.MustRelation(enginetest.RoleTableRef(t, e, "body_defaults").Address)
		persisted := view.MustRelation(enginetest.RoleTableRef(t, e, "body_view").Address)
		def := table.ColumnMeta.Defaults[table.Attributes[0]]
		return []recipe{
			{consumer: table, node: def.Expression, refs: def.Dependencies},
			{consumer: persisted, node: stored.ViewOf(persisted).Query().Root.(*plan.ProjectNode).Expressions[0], checks: stored.ViewOf(persisted).Dependencies()},
		}
	}
	authority := func(e *core.Engine) core.AuthorizationView {
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	before := source.CatalogRead()
	original := capture(source)
	sourceAuth := authority(source)
	// Save actual source manifests and declaration provenance before remapping;
	// no reconstructed/grafted expected recipe substitutes for the stored one.
	originalRefs := append([]ids.ObjectRef(nil), original[0].refs...)
	originalChecks := append([]catalog.ExpressionDependency(nil), original[1].checks...)
	sourceCall, err := exec.ExpressionRoutineDescriptor(original[0].node)
	if err != nil || sourceCall == nil {
		t.Fatal("missing original DEFAULT routine carrier", err)
	}
	sourceOwner, sourceRevision := sourceCall.Owner, sourceCall.Revision()
	target := engine.New()
	retired := target.CatalogRead()
	retiredRef := target.Catalog.Domain.Ref(retired.Epoch(), sourceCall.Address())
	change, err := target.ReplaceCatalog(ctx, source, before)
	if err != nil || !change.Replacement {
		t.Fatal("replace genuine stored catalog", change, err)
	}
	current := target.CatalogRead()
	if current.Epoch() != retired.Epoch()+1 {
		t.Fatal("replacement did not advance destination epoch")
	}
	if err := enginetest.ValidateObjectRef(target, current, retiredRef); err == nil {
		t.Fatal("retired destination routine ref survived replacement")
	}
	targetAuth := authority(target)
	imported := capture(target)
	for i, item := range imported {
		if item.consumer == original[i].consumer || item.consumer.Owner != target.RoleStore.Domain.Principal(item.consumer.Owner.ID()) {
			t.Fatal("stored consumer retained source ownership", i)
		}
		call, err := exec.ExpressionRoutineDescriptor(item.node)
		if err != nil || call == nil || call.Address() != sourceCall.Address() || call == sourceCall ||
			call.Owner != target.RoleStore.Domain.Principal(call.Owner.ID()) || call.Revision() == 0 {
			t.Fatal("stored call lost destination declaration provenance", i, err)
		}
		evaluation := exec.Context{
			EvalStatement: exec.EvalStatement{Context: ctx},
			EvalAdmission: exec.EvalAdmission{
				RoutineAuthority:  &targetAuth,
				RoutineRecipe:     true,
				RoutineConsumer:   item.consumer,
				RoutineReferences: item.refs,
				RoutineChecks:     item.checks,
			},
		}
		if err := exec.AdmitRoutineCall(&evaluation, item.node, true, false); err != nil {
			t.Fatal("remapped original recipe admission", i, err)
		}
		// The released TEXT wrapper calls octet_length(text), then int4mul.
		// Each original body ref must independently remain necessary; equal
		// builtin addresses in either engine cannot repair a missing manifest.
		for _, id := range []ids.OID{1811, 1374, 141} {
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
			probe := evaluation
			probe.RoutineReferences = []ids.ObjectRef{}
			probe.RoutineChecks = nil
			var selected ids.ObjectRef
			if i == 0 {
				for _, ref := range item.refs {
					if ref.Domain() != target.Catalog.Domain || ref.Epoch != current.Epoch() {
						t.Fatal("DEFAULT manifest retained source/old-epoch ref", ref)
					}
					if ref.Address == address {
						selected = ref
					} else {
						probe.RoutineReferences = append(probe.RoutineReferences, ref)
					}
				}
			} else {
				probe.RoutineReferences = nil
				probe.RoutineChecks = []catalog.ExpressionDependency{}
				for _, dependency := range item.checks {
					ref := dependency.Ref
					if ref.Domain() != target.Catalog.Domain || ref.Epoch != current.Epoch() {
						t.Fatal("view manifest retained source/old-epoch ref", ref)
					}
					if ref.Address == address {
						selected = ref
						d, ok := current.Descriptor(address)
						if !ok || d.Revision() != dependency.Revision {
							t.Fatal("view manifest lost original declaration revision", address)
						}
					} else {
						probe.RoutineChecks = append(probe.RoutineChecks, dependency)
					}
				}
			}
			if selected.Domain() == nil {
				t.Fatal("stored original manifest omitted released body identity", i, id)
			}
			if err := enginetest.ValidateObjectRef(target, current, selected); err != nil {
				t.Fatal("remapped original ref invalid", err)
			}
			if err := exec.AdmitRoutineCall(&probe, item.node, true, false); err == nil {
				t.Fatal("missing original body ref was rescued", i, id)
			}
		}
		foreign := evaluation
		foreign.RoutineAuthority = &sourceAuth
		if err := exec.AdmitRoutineCall(&foreign, item.node, true, false); err == nil {
			t.Fatal("destination recipe borrowed source authority", i)
		}
		old := evaluation
		old.RoutineReferences, old.RoutineChecks = original[i].refs, original[i].checks
		if err := exec.AdmitRoutineCall(&old, item.node, true, false); err == nil {
			t.Fatal("destination recipe borrowed same-address source manifest", i)
		}
		if i == 1 {
			stale := evaluation
			stale.RoutineChecks = append([]catalog.ExpressionDependency(nil), item.checks...)
			for j := range stale.RoutineChecks {
				if stale.RoutineChecks[j].Ref.Address == call.Address() {
					stale.RoutineChecks[j].Revision++
				}
			}
			if err := exec.AdmitRoutineCall(&stale, item.node, true, false); err == nil {
				t.Fatal("view accepted mismatched original routine revision")
			}
		}
	}
	after := capture(source)
	if source.CatalogRead().State() != before.State() || sourceCall.Owner != sourceOwner || sourceCall.Revision() != sourceRevision ||
		!reflect.DeepEqual(after[0].refs, originalRefs) || !reflect.DeepEqual(after[1].checks, originalChecks) {
		t.Fatal("replacement or negative probes mutated source provenance")
	}
	for i, item := range after {
		if item.consumer != original[i].consumer || !reflect.DeepEqual(item.node, original[i].node) {
			t.Fatal("replacement mutated original stored recipe", i)
		}
		evaluation := exec.Context{
			EvalStatement: exec.EvalStatement{Context: ctx},
			EvalAdmission: exec.EvalAdmission{
				RoutineAuthority:  &sourceAuth,
				RoutineRecipe:     true,
				RoutineConsumer:   item.consumer,
				RoutineReferences: item.refs,
				RoutineChecks:     item.checks,
			},
		}
		if err := exec.AdmitRoutineCall(&evaluation, item.node, true, false); err != nil {
			t.Fatal("source original recipe lost admission", i, err)
		}
	}
}
