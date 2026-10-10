package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExclusionPublication(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(n INT,CONSTRAINT ex EXCLUDE USING "+method+"((n+0) WITH =))")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			parent := e.state.descriptors[roleTableRef(t, e, "public.t").Address].(*relationDescriptor)
			constraint := parent.exclusionConstraints[0]
			index := e.state.descriptors[constraint.Index].(*relationDescriptor)
			store := e.storage[parent.address]
			value, err := parent.columns[0].Usage.Type.Input(context.Background(), "1", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, materialize := range []bool{false, true} {
				before := indexMaintenanceCapture(e)
				storeBefore := uniquePublicationStore(store)
				row, version := e.nextRow, e.nextVersion
				writes := []rowVersion{{row: RowID(row + 1), version: VersionID(version + 1), values: []sqltype.Value{value}}}
				publication := rowPublication{address: parent.address, relation: parent, originalRelation: parent, original: store, store: store, root: store.versions, ends: store.ends, generation: store.generation, writes: writes, singleSlot: -1}
				if materialize {
					if err := e.prepareRowPublication(&publication, TxRef{owner: e, epoch: e.state.epoch}, true); err != nil {
						t.Fatal(err)
					}
				}
				roots, err := e.prepareIndexPublication(context.Background(), e.state, e.state, captureIndexStores(e.storage, e.state), e.indexRoots, map[ObjectAddress]bool{parent.address: false}, nil, []rowPublication{publication}, materialize, sqltype.OutputEnvironment{}, nil)
				var diagnostic *sqlstate.Error
				if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" || diagnostic.Constraint != "ex" {
					t.Fatal("publication omitted the actual exclusion integrity veto", materialize, roots, err)
				}
				indexMaintenanceUnchanged(t, e, before)
				if e.nextRow != row || e.nextVersion != version || !reflect.DeepEqual(*store, storeBefore) {
					t.Fatal("failed package preparation changed immutable inputs")
				}
			}
			for _, cancelled := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				fault := errors.New("exclusion derived provider fault")
				next := *e.state
				next.descriptors = maps.Clone(e.state.descriptors)
				indexCopy, definition := *index, *index.sqlIndex
				definition.keys = slices.Clone(definition.keys)
				derived := *definition.keys[0].derived
				called := false
				derived.execution = queryProbe(derived.execution.ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
					called = true
					if !e.mu.TryLock() {
						t.Error("exclusion publication invoked runtime provider under engine lock")
						return sqltype.Value{}, fault
					}
					e.mu.Unlock()
					if cancelled {
						cancel()
						return value, ctx.Err()
					}
					return sqltype.Value{}, fault
				})
				definition.keys[0].derived = &derived
				indexCopy.sqlIndex = &definition
				next.descriptors[index.address] = &indexCopy
				before := indexMaintenanceCapture(e)
				roots, err := e.prepareIndexPublication(ctx, e.state, &next, captureIndexStores(e.storage, &next), e.indexRoots, map[ObjectAddress]bool{parent.address: true}, nil, nil, true, sqltype.OutputEnvironment{}, nil)
				cancel()
				want := fault
				if cancelled {
					want = context.Canceled
				}
				if !called || roots != nil || !errors.Is(err, want) {
					t.Fatal("derived failure did not veto complete root preparation", called, roots, err)
				}
				indexMaintenanceUnchanged(t, e, before)
			}
		})
		t.Run(method+"/full_diagnostic", func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE SCHEMA exclusion_publication")
			sessionSQL(t, s, "CREATE TABLE exclusion_publication.t(n INT,CONSTRAINT ex EXCLUDE USING "+method+"(n WITH =))")
			sessionSQL(t, s, "INSERT INTO exclusion_publication.t VALUES(1),(2)")
			parent := e.state.descriptors[roleTableRef(t, e, "exclusion_publication.t").Address].(*relationDescriptor)
			store := e.storage[parent.address]
			value, err := parent.columns[0].Usage.Type.Input(context.Background(), "1", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, materialize := range []bool{false, true} {
				t.Run(map[bool]string{false: "nonmaterializing", true: "materializing"}[materialize], func(t *testing.T) {
					before := indexMaintenanceCapture(e)
					storeBefore := uniquePublicationStore(store)
					row, version := e.nextRow, e.nextVersion
					writes := []rowVersion{{row: RowID(row + 1), version: VersionID(version + 1), values: []sqltype.Value{value}}}
					publication := rowPublication{address: parent.address, relation: parent, originalRelation: parent, original: store, store: store, root: store.versions, ends: store.ends, generation: store.generation, writes: writes, singleSlot: -1}
					if materialize {
						if err := e.prepareRowPublication(&publication, TxRef{owner: e, epoch: e.state.epoch}, true); err != nil {
							t.Fatal(err)
						}
					}
					roots, err := e.prepareIndexPublication(context.Background(), e.state, e.state, captureIndexStores(e.storage, e.state), e.indexRoots, map[ObjectAddress]bool{parent.address: false}, nil, []rowPublication{publication}, materialize, sqltype.OutputEnvironment{}, nil)
					if roots != nil {
						t.Fatal("integrity veto returned publication roots", roots)
					}
					indexMaintenanceUnchanged(t, e, before)
					if e.nextRow != row || e.nextVersion != version || !reflect.DeepEqual(*store, storeBefore) {
						t.Fatal("integrity veto changed immutable publication inputs")
					}
					nullCountUnitDiagnostic(t, err, sqlstate.Error{
						Code:       "23P01",
						Message:    `conflicting key value violates exclusion constraint "ex"`,
						Detail:     "Key (n)=(1) conflicts with existing key (n)=(1).",
						Schema:     "exclusion_publication",
						Table:      "t",
						Constraint: "ex",
					})
				})
			}
		})
	}
}

func TestExclusionPublicationFinalCancellation(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(n INT,CONSTRAINT ex EXCLUDE USING "+method+"(n WITH =))")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			counting := &cancelAtErrCheck{Context: ctx, cancel: cancel}
			first := indexMaintenanceStage(t, e, "t", "ex")
			prepared, err := e.ValidateCatalogStage(counting, first)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: first.base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			polls := counting.checks.Load()
			if polls < 4 {
				t.Fatal("missing real root preparation/final veto", polls)
			}
			vetoContext, vetoCancel := context.WithCancel(context.Background())
			defer vetoCancel()
			veto := &cancelAtErrCheck{Context: vetoContext, cancel: vetoCancel, at: polls}
			second := indexMaintenanceStage(t, e, "t", "ex")
			prepared, err = e.ValidateCatalogStage(veto, second)
			if err != nil {
				t.Fatal("cancellation occurred before package preparation", err)
			}
			before := indexMaintenanceCapture(e)
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: second.base.Epoch()}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}
