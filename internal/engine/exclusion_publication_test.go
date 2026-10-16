package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExclusionPublication(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(n INT,CONSTRAINT ex EXCLUDE USING "+method+"((n+1) WITH =))")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			parent := e.state.descriptors[roleTableRef(t, e, "public.t").Address].(*relationDescriptor)
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
				roots, err := e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, e.state, e.state, captureIndexStores(e.storage, e.state), e.indexRoots, map[ObjectAddress]bool{parent.address: false}, nil, []rowPublication{publication}, materialize, sqltype.OutputEnvironment{}, nil, nil, nil)
				var diagnostic *sqlstate.Error
				if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" || diagnostic.Constraint != "ex" {
					t.Fatal("publication omitted the actual exclusion integrity veto", materialize, roots, err)
				}
				indexMaintenanceUnchanged(t, e, before)
				if e.nextRow != row || e.nextVersion != version || !reflect.DeepEqual(*store, storeBefore) {
					t.Fatal("failed package preparation changed immutable inputs")
				}
			}
			// Use a supported original recipe and a real INT4 input: the
			// derived addition must fail before any publication is installed.
			overflow, err := parent.columns[0].Usage.Type.Input(context.Background(), "2147483647", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, cancelled := range []bool{false, true} {
				// Reuse the existing context checkpoint seam to observe
				// outside-lock preparation and exercise cancellation without
				// injecting a node unsupported by stored-recipe rebinding.
				called := false
				ctx := &castPreflightContext{
					Context: context.Background(), e: e, t: t, armed: true,
					visit: func() { called = true },
				}
				if cancelled {
					ctx.cancelAt = "preflight"
				}
				before := indexMaintenanceCapture(e)
				storeBefore := uniquePublicationStore(store)
				row, version := e.nextRow, e.nextVersion
				writes := []rowVersion{{row: RowID(row + 1), version: VersionID(version + 1), values: []sqltype.Value{overflow}}}
				publication := rowPublication{address: parent.address, relation: parent, originalRelation: parent, original: store, store: store, root: store.versions, ends: store.ends, generation: store.generation, writes: writes, singleSlot: -1}
				if err := e.prepareRowPublication(&publication, TxRef{owner: e, epoch: e.state.epoch}, true); err != nil {
					t.Fatal(err)
				}
				roots, err := e.prepareIndexPublication(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, e.state, e.state, captureIndexStores(e.storage, e.state), e.indexRoots, map[ObjectAddress]bool{parent.address: false}, nil, []rowPublication{publication}, true, sqltype.OutputEnvironment{}, nil, nil, nil)
				if !called || roots != nil {
					t.Fatal("derived failure did not veto outside-lock root preparation", called, roots, err)
				}
				if cancelled {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation did not veto root preparation", err)
					}
				} else {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "integer out of range" {
						t.Fatal("derived arithmetic failure did not veto root preparation", err)
					}
				}
				indexMaintenanceUnchanged(t, e, before)
				if e.nextRow != row || e.nextVersion != version || !reflect.DeepEqual(*store, storeBefore) {
					t.Fatal("derived veto changed immutable publication inputs")
				}
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
					roots, err := e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, e.state, e.state, captureIndexStores(e.storage, e.state), e.indexRoots, map[ObjectAddress]bool{parent.address: false}, nil, []rowPublication{publication}, materialize, sqltype.OutputEnvironment{}, nil, nil, nil)
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
