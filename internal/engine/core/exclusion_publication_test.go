package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExclusionPublication(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(n INT,CONSTRAINT ex EXCLUDE USING "+method+"((n+1) WITH =))")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			parent := e.Catalog.State.MustRelation(roleTableRef(t, e, "public.t").Address)
			store := e.Storage.Relations[parent.Address()]
			value, err := parent.Columns[0].Usage.Type.Input(context.Background(), "1", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, materialize := range []bool{false, true} {
				before := indexMaintenanceCapture(e)
				storeBefore := uniquePublicationStore(store)
				row, version := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
				writes := []storage.RowVersion{{Row: ids.RowID(row + 1), Version: ids.VersionID(version + 1), Values: []sqltype.Value{value}}}
				publication := index.RowPublication{Address: parent.Address(), Relation: parent, OriginalRelation: parent, Original: store, Store: store, Root: store.Versions, Ends: store.Ends, Generation: store.Generation, Writes: writes, SingleSlot: -1}
				if materialize {
					if err := index.PrepareRowPublication(&publication, e.Catalog.Domain, e.Catalog.Domain.Tx(ids.SessionToken{}, 0, e.Catalog.State.Epoch(), nil, 0), true); err != nil {
						t.Fatal(err)
					}
				}
				roots, err := core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, e.Catalog.State, e.Catalog.State, index.CaptureIndexStores(e.Storage.Relations, e.Catalog.State), e.Storage.IndexRoots, map[ids.ObjectAddress]bool{parent.Address(): false}, nil, []index.RowPublication{publication}, materialize, sqltype.OutputEnvironment{}, nil, nil, nil)
				var diagnostic *sqlstate.Error
				if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" || diagnostic.Constraint != "ex" {
					t.Fatal("publication omitted the actual exclusion integrity veto", materialize, roots, err)
				}
				indexMaintenanceUnchanged(t, e, before)
				if e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version || !reflect.DeepEqual(*store, storeBefore) {
					t.Fatal("failed package preparation changed immutable inputs")
				}
			}
			// Use a supported original recipe and a real INT4 input: the
			// derived addition must fail before any publication is installed.
			overflow, err := parent.Columns[0].Usage.Type.Input(context.Background(), "2147483647", -1)
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
				row, version := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
				writes := []storage.RowVersion{{Row: ids.RowID(row + 1), Version: ids.VersionID(version + 1), Values: []sqltype.Value{overflow}}}
				publication := index.RowPublication{Address: parent.Address(), Relation: parent, OriginalRelation: parent, Original: store, Store: store, Root: store.Versions, Ends: store.Ends, Generation: store.Generation, Writes: writes, SingleSlot: -1}
				if err := index.PrepareRowPublication(&publication, e.Catalog.Domain, e.Catalog.Domain.Tx(ids.SessionToken{}, 0, e.Catalog.State.Epoch(), nil, 0), true); err != nil {
					t.Fatal(err)
				}
				roots, err := core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, e.Catalog.State, e.Catalog.State, index.CaptureIndexStores(e.Storage.Relations, e.Catalog.State), e.Storage.IndexRoots, map[ids.ObjectAddress]bool{parent.Address(): false}, nil, []index.RowPublication{publication}, true, sqltype.OutputEnvironment{}, nil, nil, nil)
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
				if e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version || !reflect.DeepEqual(*store, storeBefore) {
					t.Fatal("derived veto changed immutable publication inputs")
				}
			}
		})
		t.Run(method+"/full_diagnostic", func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE SCHEMA exclusion_publication")
			sessionSQL(t, s, "CREATE TABLE exclusion_publication.t(n INT,CONSTRAINT ex EXCLUDE USING "+method+"(n WITH =))")
			sessionSQL(t, s, "INSERT INTO exclusion_publication.t VALUES(1),(2)")
			parent := e.Catalog.State.MustRelation(roleTableRef(t, e, "exclusion_publication.t").Address)
			store := e.Storage.Relations[parent.Address()]
			value, err := parent.Columns[0].Usage.Type.Input(context.Background(), "1", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, materialize := range []bool{false, true} {
				t.Run(map[bool]string{false: "nonmaterializing", true: "materializing"}[materialize], func(t *testing.T) {
					before := indexMaintenanceCapture(e)
					storeBefore := uniquePublicationStore(store)
					row, version := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
					writes := []storage.RowVersion{{Row: ids.RowID(row + 1), Version: ids.VersionID(version + 1), Values: []sqltype.Value{value}}}
					publication := index.RowPublication{Address: parent.Address(), Relation: parent, OriginalRelation: parent, Original: store, Store: store, Root: store.Versions, Ends: store.Ends, Generation: store.Generation, Writes: writes, SingleSlot: -1}
					if materialize {
						if err := index.PrepareRowPublication(&publication, e.Catalog.Domain, e.Catalog.Domain.Tx(ids.SessionToken{}, 0, e.Catalog.State.Epoch(), nil, 0), true); err != nil {
							t.Fatal(err)
						}
					}
					roots, err := core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, e.Catalog.State, e.Catalog.State, index.CaptureIndexStores(e.Storage.Relations, e.Catalog.State), e.Storage.IndexRoots, map[ids.ObjectAddress]bool{parent.Address(): false}, nil, []index.RowPublication{publication}, materialize, sqltype.OutputEnvironment{}, nil, nil, nil)
					if roots != nil {
						t.Fatal("integrity veto returned publication roots", roots)
					}
					indexMaintenanceUnchanged(t, e, before)
					if e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version || !reflect.DeepEqual(*store, storeBefore) {
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
			e := engine.New()
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
			if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: first.Base.Epoch()}); err != nil {
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
			if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: second.Base.Epoch()}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}

func indexMaintenanceCapture(e *core.Engine) indexMaintenanceSnapshot {
	s := indexMaintenanceSnapshot{catalog: e.Catalog.State, roots: make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots)), stores: make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))}
	for a, r := range e.Storage.IndexRoots {
		s.roots[a] = r
	}
	for a, r := range e.Storage.Relations {
		s.stores[a] = r
	}
	return s
}

func indexMaintenanceUnchanged(t *testing.T, e *core.Engine, before indexMaintenanceSnapshot) {
	t.Helper()
	if e.Catalog.State != before.catalog || len(e.Storage.IndexRoots) != len(before.roots) || len(e.Storage.Relations) != len(before.stores) {
		t.Fatal("failed operation published catalog/storage/root state")
	}
	for a, r := range before.roots {
		if e.Storage.IndexRoots[a] != r {
			t.Fatal("failed operation changed root", a)
		}
	}
	for a, r := range before.stores {
		if e.Storage.Relations[a] != r {
			t.Fatal("failed operation changed table storage", a)
		}
	}
}

func indexMaintenanceStage(t *testing.T, e *core.Engine, table string, indexes ...string) *core.CatalogStage {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	parent := roleTableRef(t, e, table)
	stage.Indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address: base.MustRelation(parent.Address)}
	stage.Indexes().Rebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(indexes))
	for _, name := range indexes {
		ref := roleTableRef(t, e, name)
		stage.Indexes().Rebuilds[ref.Address] = base.MustRelation(ref.Address)
	}
	return stage
}

type indexMaintenanceSnapshot struct {
	catalog *catalog.State
	roots   map[ids.ObjectAddress]*storage.IndexRoot
	stores  map[ids.ObjectAddress]*storage.Relation
}
