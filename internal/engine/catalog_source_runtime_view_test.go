package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
)

func TestCatalogSourceRuntimeViewReferencesConsumer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.state)
	target := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "runtime_target", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	target.RelationKind = catalog.TableRelation
	state.SetDescriptor(target.Address(), target)
	ref := f.e.catalog.Domain.Ref(state.Epoch(), target.Address())
	consumer := *target
	consumer.ObjectAddress.ObjectID++
	consumer.ObjectName = "runtime_consumer"
	consumer.RelationKind = catalog.ViewRelation
	consumer.View = stored.NewView(stored.ViewParts{
		Query:        &plan.Query{Root: &plan.SourceNode{Ref: ref}},
		Sources:      []stored.ViewLeaf{{Ref: ref}},
		Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: 1}},
	})
	state.SetDescriptor(consumer.Address(), &consumer)
	view := f.e.catalog.read(state)
	copied := consumer
	missing := consumer
	missing.ObjectAddress.ObjectID++
	missing.ObjectName = "runtime_missing_definition"
	missing.View = nil
	typedNil := missing
	typedNil.ObjectAddress.ObjectID++
	typedNil.ObjectName = "runtime_typed_nil_definition"
	typedNil.View = (*stored.View)(nil)
	invalidState := catalog.CloneState(state)
	invalidState.SetDescriptor(missing.Address(), &missing)
	invalidState.SetDescriptor(typedNil.Address(), &typedNil)
	invalidView := f.e.catalog.read(invalidState)

	// Both snapshots have the same domain/epoch and valid reference targets.
	// A consumer live only in the earlier snapshot must not borrow the newer
	// snapshot's authority, even though every manifest reference is valid there.
	nextState := catalog.CloneState(state)
	replacement := consumer
	replacement.ObjectName = "runtime_replacement"
	nextState.SetDescriptor(replacement.Address(), &replacement)
	nextView := f.e.catalog.read(nextState)
	if ref.Epoch != nextView.Epoch() || !stored.ViewReferenceValid(nextView, &consumer, ref) {
		t.Fatal("borrowed-consumer fixture lacks a current-catalog-valid manifest")
	}

	// pg_depend calls this seam without an authorization capture. The explicit
	// catalog, not runtime.auth.Catalog, owns the descriptor/definition frontier.
	runtime := engineCatalogRuntime{}
	for _, tc := range []struct {
		name     string
		view     catalog.Read
		consumer *catalog.Relation
		valid    bool
	}{
		{"nil_relation", view, nil, false},
		{"missing_definition", invalidView, &missing, false},
		{"typed_nil_definition", invalidView, &typedNil, false},
		{"copied_descriptor", view, &copied, false},
		{"borrowed_consumer", nextView, &consumer, false},
		{"live_consumer", nextView, &replacement, true},
		{"captured_catalog", view, &consumer, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Report invalid-input panics as regression failures without hiding
			// the remaining independently selected consumer-boundary cases.
			defer func() {
				if failure := recover(); failure != nil {
					t.Errorf("ViewReferences panicked instead of returning an error: %v", failure)
				}
			}()
			refs, err := runtime.ViewReferences(t.Context(), tc.view, tc.consumer)
			if tc.valid {
				if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{target.Address()}) {
					t.Fatal("valid live definition lost direct references", refs, err)
				}
			} else if refs != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("invalid consumer accepted or returned a reference prefix", refs, err)
			}
		})
		t.Run(tc.name+"_canceled", func(t *testing.T) {
			defer func() {
				if failure := recover(); failure != nil {
					t.Errorf("ViewReferences panicked instead of honoring cancellation: %v", failure)
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if refs, err := runtime.ViewReferences(ctx, tc.view, tc.consumer); refs != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation did not precede consumer validation", refs, err)
			}
		})
	}
}

func TestCatalogSourceRuntimeViewReferencesRetainedTemporary(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TEMP TABLE runtime_retained_base(v INT)")
	sessionSQL(t, owner, "CREATE TEMP VIEW runtime_retained_view AS SELECT v FROM runtime_retained_base")
	before := e.CatalogRead()
	address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "runtime_retained_view"})
	consumer := before.MustRelation(address)
	target := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "runtime_retained_base"})
	target.SubID = 1
	for _, tc := range []struct {
		name    string
		replace func() error
	}{
		{"reset", func() error { return e.Reset(t.Context()) }},
		{"restore", func() error { return e.Restore(t.Context(), strings.NewReader("CREATE TABLE runtime_marker(v INT);")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.replace(); err != nil {
				t.Fatal(err)
			}
			current := e.CatalogRead()
			if current.Get(address) != consumer || current.Epoch() <= before.Epoch() {
				t.Fatal("fixture did not retain the exact old-epoch TEMP consumer")
			}
			definition := stored.ViewOf(consumer)
			if len(definition.Sources()) != 1 || definition.Sources()[0].Ref.Epoch != before.Epoch() {
				t.Fatal("fixture lost the retained source manifest")
			}
			refs, err := (engineCatalogRuntime{}).ViewReferences(t.Context(), current, consumer)
			if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{target}) {
				t.Fatal("retained TEMP definition lost direct references", refs, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if refs, err := (engineCatalogRuntime{}).ViewReferences(ctx, current, consumer); refs != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("retained TEMP cancellation exposed references", refs, err)
			}
		})
	}
}
