package viewcmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func TestMaterializedRefreshConcurrentReaderAdmission(t *testing.T) {
	e := engine.New()
	d := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.Catalog.State.MustRelation(d.Address())
	original := e.Storage.Relations[d.Address()]
	scope := refreshUnitScope(t, e, context.Background())
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	scope.QuerySources = []exec.Leaf{{Ref: ref, Relation: d, Storage: original}, {Ref: ref, Relation: d, Storage: original}}
	d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 4), true, true)
	current := e.Storage.Relations[d.Address()]
	e.Mu.Lock()
	err := scope.RefreshRelationGrantsLocked(e)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("admission-to-publication recapture", err)
	}
	for _, source := range scope.QuerySources {
		if source.Relation != d || source.Storage != current {
			t.Fatal("aliases did not recapture one coherent manifest")
		}
	}
	e.Mu.Lock()
	view, err := scope.AcquireDataViewsLocked(e, []ids.ObjectRef{ref, ref})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := view.Data()
	frontier := retained.Frontier
	if !storage.SameVersionRoot(retained.Committed[d.Address()], current.Versions) {
		t.Fatal("capture did not own current immutable root")
	}
	next := concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 2), true, true)
	e.Mu.Lock()
	err = scope.RefreshRelationGrantsLocked(e)
	if err == nil {
		err = scope.ValidateLockedRelation(e)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("captured reader rejected compatible publication", err)
	}
	again, err := scope.AcquireDataView(ref)
	if err != nil || again != view {
		t.Fatal("captured data view changed", err)
	}
	if again.Data().Frontier != frontier || !storage.SameVersionRoot(again.Data().Committed[d.Address()], current.Versions) || len(again.Data().Committed[d.Address()]) != 4 {
		t.Fatal("reader mixed incarnations")
	}
	for _, source := range scope.QuerySources {
		if source.Relation != d || source.Storage != current {
			t.Fatal("postcapture layout/storage was recaptured")
		}
	}
	// Shared token alone cannot authorize a metadata/owner rewrite.
	incompatible := *next
	incompatible.ObjectName = "renamed"
	e.Mu.Lock()
	e.Catalog.State.SetDescriptor(next.Address(), &incompatible)
	err = scope.ValidateLockedRelation(e)
	e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("defining authority fence weakened", err)
	}
}

func TestMaterializedRefreshConcurrentSnapshots(t *testing.T) {
	// These synthetic descriptor controls exercise two different consumers:
	// first demand after SnapshotPhase, versus an already installed View.
	change := func(d *catalog.Relation, phase string) *catalog.Relation {
		copy := *d
		switch phase {
		case "owner":
			copy.Owner = authorization.PrincipalRef{} // Deliberate corruption, not admitted OWNER DDL.
		case "schema":
			copy.ObjectNamespace.ObjectID++ // Deliberate corruption, not admitted namespace DDL.
		case "name":
			copy.ObjectName = "renamed"
		case "view":
			definition := *stored.ViewOf(d)
			copy.View = &definition
		case "columns":
			copy.Columns = slices.Clone(copy.Columns)
		case "attributes":
			copy.Attributes = slices.Clone(copy.Attributes)
		case "column_value":
			copy.Columns = slices.Clone(copy.Columns)
			copy.Columns[0].Name = "different_column"
		case "attribute_value":
			copy.Attributes = slices.Clone(copy.Attributes)
			copy.Attributes[0]++
		case "rowtype":
			copy.RowType++
		case "method":
			copy.TableMethod = 403 // Distinct existing AM identity; no new table-AM support.
		case "ordinary_table":
			copy.RelationKind = catalog.TableRelation
		default:
			t.Fatal("unknown descriptor control", phase)
		}
		return &copy
	}
	for _, beforeCapture := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured_first", true: "database_snapshot_before_first_demand"}[beforeCapture], func(t *testing.T) {
			e := engine.New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.Catalog.State.MustRelation(d.Address())
			stage, unrelated := queryCreateStage(t, e, "unrelated_snapshot", 1)
			queryCreatePublish(t, e, stage)
			original := e.Storage.Relations[d.Address()]
			original.Ends = &storage.DeletionRoot{}
			scope := refreshUnitScope(t, e, context.Background())
			scope.Owner.Isolation = session.RepeatableRead
			if err := core.ScopeSnapshotPhase(scope, &parser.Select{}); err != nil {
				t.Fatal(err)
			}
			root := scope.Owner.Roots[d.Address()]
			frontier := scope.Owner.Frontier
			if root.Storage != original || root.Ends != original.Ends || scope.Owner.Roots[unrelated.Address()].Storage != e.Storage.Relations[unrelated.Address()] {
				t.Fatal("snapshot did not capture database roots")
			}
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
			if !beforeCapture {
				if _, err := scope.AcquireDataView(ref); err != nil {
					t.Fatal(err)
				}
			}
			for _, count := range []int{4, 1} {
				d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, count), true, true)
				if scope.View != nil {
					scope.View.Release()
					scope.View = nil
				}
				view, err := scope.AcquireDataView(ref)
				if err != nil {
					t.Fatal("RR compatible root selection", err)
				}
				data := view.Data()
				if data.Frontier != frontier || !storage.SameVersionRoot(data.Committed[d.Address()], root.Versions) || data.Ends[d.Address()] != root.Ends || data.NeedsBirthFilterAt(d.Address()) || len(data.Committed[d.Address()]) != 3 {
					t.Fatal("RR selected current/birth-filtered rows")
				}
			}
		})
	}
	for _, phase := range []string{"plain_data", "plain_no_data", "control_epoch", "ordinary_incarnation", "view", "column_value", "attribute_value", "rowtype", "method", "ordinary_table"} {
		t.Run("reject_"+phase, func(t *testing.T) {
			e := engine.New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.Catalog.State.MustRelation(d.Address())
			scope := refreshUnitScope(t, e, context.Background())
			scope.Owner.Isolation = session.RepeatableRead
			if err := core.ScopeSnapshotPhase(scope, &parser.Select{}); err != nil {
				t.Fatal(err)
			}
			originalEpoch := e.Catalog.State.Epoch()
			defer func() { e.Catalog.State.SetEpoch(originalEpoch) }()
			ref := e.Catalog.Domain.Ref(originalEpoch, d.Address())
			root, frontier := scope.Owner.Roots[d.Address()], scope.Owner.Frontier
			oldDescriptor, oldRows := *root.Relation, slices.Clone(root.Versions)
			for i := range oldRows { oldRows[i].Values = slices.Clone(oldRows[i].Values) }
			oldOrigin, oldBoundary := root.Storage.RetainedOrigin, root.Storage.TruncateBoundary
			d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 4), true, true)
			switch phase {
			case "plain_data":
				d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 2), false, true)
			case "plain_no_data":
				d = concurrentUnitReplace(t, e, d, nil, false, false)
			case "control_epoch":
				e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
			case "ordinary_incarnation":
				previous := e.Storage.Relations[d.Address()]
				if previous.RetainedOrigin == 0 || previous.MaterializedLineage == nil {
					t.Fatal("ordinary-incarnation control lacks genuine concurrent provenance")
				}
				copy := *previous
				// A real ordinary replacement constructs a fresh store, not a
				// compatible-family successor. Preserve this fixture's row/root
				// contents but clear all inherited read-history provenance.
				copy.RetainedOrigin = 0
				copy.TruncateBoundary = 0
				copy.MaterializedLineage = nil
				copy.TruncateLineage = nil
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(d.Address(), &copy)
				e.Mu.Unlock()
				if copy.RetainedOrigin != 0 || copy.TruncateBoundary != 0 || copy.MaterializedLineage != nil || copy.TruncateLineage != nil || previous.RetainedOrigin == 0 || previous.MaterializedLineage == nil {
					t.Fatal("ordinary-incarnation control inherited provenance or changed its predecessor")
				}
			default:
				e.Catalog.State.SetDescriptor(d.Address(), change(d, phase))
			}
			view, err := scope.AcquireDataView(ref)
			if view != nil || scope.View != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("incompatible snapshot replacement bridged lineage", phase, err)
			}
			retained := scope.Owner.Roots[ref.Address]
			if scope.Owner.Frontier != frontier || retained.Storage != root.Storage || retained.Relation != root.Relation || !storage.SameVersionRoot(retained.Versions, root.Versions) || retained.Ends != root.Ends || !reflect.DeepEqual(root.Versions, oldRows) || !reflect.DeepEqual(*root.Relation, oldDescriptor) || root.Storage.RetainedOrigin != oldOrigin || root.Storage.TruncateBoundary != oldBoundary {
				t.Fatal("first-demand rejection mutated original owned roots/descriptor/provenance")
			}
		})
	}
	for _, phase := range []string{"columns", "attributes"} {
		t.Run("first_demand_equal_"+phase+"_backing", func(t *testing.T) {
			e := engine.New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.Catalog.State.MustRelation(d.Address())
			scope := refreshUnitScope(t, e, context.Background())
			scope.Owner.Isolation = session.RepeatableRead
			if err := core.ScopeSnapshotPhase(scope, &parser.Select{}); err != nil { t.Fatal(err) }
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
			root, frontier := scope.Owner.Roots[d.Address()], scope.Owner.Frontier
			oldRows := slices.Clone(root.Versions)
			d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 4), true, true)
			current := change(d, phase)
			e.Catalog.State.SetDescriptor(d.Address(), current)
			if scope.View != nil || current.View != root.Relation.View || current.RowType != root.Relation.RowType || current.TableMethod != root.Relation.TableMethod || !view.SameStableReadLineage(root.Relation, current, root.Storage, e.Storage.Relations[d.Address()]) || view.SameRetainedLineage(root.Relation, current, root.Storage, e.Storage.Relations[d.Address()]) {
				t.Fatal("equal backing changed structural/family authority or borrowed strict authority")
			}
			captured, err := scope.AcquireDataView(ref)
			if err != nil { t.Fatal("equal backing rejected first demand", err) }
			data := captured.Data()
			if data.Frontier != frontier || !storage.SameVersionRoot(data.Committed[d.Address()], root.Versions) || data.Ends[d.Address()] != root.Ends || data.NeedsBirthFilterAt(d.Address()) || !reflect.DeepEqual(root.Versions, oldRows) {
				t.Fatal("equal backing selected current rows or changed the exact old roots/frontier")
			}
		})
	}
	for _, phase := range []string{"compatible", "owner", "schema", "name", "columns", "attributes", "view", "rowtype", "method", "column_value", "attribute_value"} {
		t.Run("captured_authority_"+phase, func(t *testing.T) {
			e := engine.New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.Catalog.State.MustRelation(d.Address())
			scope := refreshUnitScope(t, e, context.Background())
			scope.Owner.Isolation = session.RepeatableRead
			if err := core.ScopeSnapshotPhase(scope, &parser.Select{}); err != nil { t.Fatal(err) }
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
			root := scope.Owner.Roots[d.Address()]
			scope.QuerySources = []exec.Leaf{{Ref: ref, Relation: d, Storage: root.Storage}}
			leaf := scope.QuerySources[0]
			// SnapshotPhase alone is not this capture. Install and retain the
			// actual view before any replacement; never clear it to rebind.
			captured, err := scope.AcquireDataView(ref)
			if err != nil { t.Fatal(err) }
			data, frontier := captured.Data(), captured.Data().Frontier
			versions, ends := data.Committed[d.Address()], data.Ends[d.Address()]
			oldRows := slices.Clone(root.Versions)
			d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 4), true, true)
			if phase != "compatible" {
				e.Catalog.State.SetDescriptor(d.Address(), change(d, phase))
			}
			again, err := scope.AcquireDataView(ref)
			_, sourceErr := view.CurrentQuerySource(e.CatalogRead(), e.Catalog.Domain, ref.Epoch, e.Storage.Relations[d.Address()], leaf)
			if phase == "compatible" {
				if err != nil || sourceErr != nil || again != captured { t.Fatal("strict unchanged-metadata captured control", err, sourceErr) }
			} else if again != nil || !errors.Is(err, ids.ErrCatalogReplaced) || !errors.Is(sourceErr, ids.ErrCatalogReplaced) {
				t.Fatal("captured view/source accepted changed defining authority", phase, err, sourceErr)
			}
			if scope.View != captured || captured.Data().Frontier != frontier || !storage.SameVersionRoot(captured.Data().Committed[d.Address()], versions) || captured.Data().Ends[d.Address()] != ends || captured.Data().NeedsBirthFilterAt(d.Address()) || !reflect.DeepEqual(root.Versions, oldRows) || scope.QuerySources[0].Relation != leaf.Relation || scope.QuerySources[0].Storage != leaf.Storage {
				t.Fatal("strict validation recaptured a source or changed owned view/root/frontier")
			}
		})
	}
	for _, action := range []string{"rename", "owner", "owner_acl_denied"} {
		t.Run("sql_current_metadata_"+action, func(t *testing.T) {
			e := engine.New()
			writer, reader := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE mv_metadata_anchor(id INT)", "INSERT INTO mv_metadata_anchor VALUES(9)",
				"CREATE TABLE mv_metadata_base(id INT PRIMARY KEY)", "INSERT INTO mv_metadata_base VALUES(1),(2)",
				"CREATE MATERIALIZED VIEW mv_metadata_target AS SELECT id FROM mv_metadata_base",
				"CREATE UNIQUE INDEX mv_metadata_index ON mv_metadata_target(id)",
				"CREATE ROLE mv_metadata_owner", "CREATE ROLE mv_metadata_reader",
				"GRANT USAGE,CREATE ON SCHEMA public TO mv_metadata_owner",
				"GRANT USAGE ON SCHEMA public TO mv_metadata_reader",
				"REVOKE ALL ON mv_metadata_anchor,mv_metadata_target FROM PUBLIC",
				"GRANT SELECT ON mv_metadata_anchor,mv_metadata_target TO mv_metadata_reader",
			} { enginetest.SessionSQL(t, writer, q) }
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "mv_metadata_target")
			enginetest.SessionSQL(t, reader, "SET ROLE mv_metadata_reader")
			enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			enginetest.TransactionRows(t, reader, "SELECT id FROM mv_metadata_anchor", 9).Close()
			root, frontier := reader.Transactions.Roots[ref.Address], reader.Transactions.Frontier
			oldDescriptor, oldRows := *root.Relation, slices.Clone(root.Versions)
			for i := range oldRows { oldRows[i].Values = slices.Clone(oldRows[i].Values) }
			enginetest.SessionSQL(t, writer, "UPDATE mv_metadata_base SET id=3 WHERE id=1")
			enginetest.SessionSQL(t, writer, "REFRESH MATERIALIZED VIEW CONCURRENTLY mv_metadata_target")
			current := e.Storage.Relations[ref.Address]
			postRefresh := e.CatalogRead().MustRelation(ref.Address)
			indexRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "mv_metadata_index")
			indexRoot, indexLocator := e.Storage.IndexRoots[indexRef.Address], e.CatalogRead().MustRelation(indexRef.Address).StorageLocator
			name := "mv_metadata_target"
			if action == "rename" {
				enginetest.SessionSQL(t, writer, "ALTER MATERIALIZED VIEW mv_metadata_target RENAME TO mv_metadata_renamed")
				name = "mv_metadata_renamed"
			} else {
				enginetest.SessionSQL(t, writer, "ALTER MATERIALIZED VIEW mv_metadata_target OWNER TO mv_metadata_owner")
			}
			descriptor := e.CatalogRead().MustRelation(ref.Address)
			if descriptor == postRefresh || descriptor.StorageLocator != postRefresh.StorageLocator || descriptor.View != root.Relation.View || descriptor.RowType != root.Relation.RowType || descriptor.TableMethod != root.Relation.TableMethod || e.Storage.Relations[ref.Address] != current || e.Storage.IndexRoots[indexRef.Address] != indexRoot || e.CatalogRead().MustRelation(indexRef.Address).StorageLocator != indexLocator || !view.SameStableReadLineage(root.Relation, descriptor, root.Storage, current) || view.SameRetainedLineage(root.Relation, descriptor, root.Storage, current) {
				t.Fatal("actual SQL metadata changed structural continuity, roots or locators")
			}
			if _, err := view.CurrentQuerySource(e.CatalogRead(), e.Catalog.Domain, ref.Epoch, current, exec.Leaf{Ref: ref, Relation: root.Relation, Storage: root.Storage}); !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("new SQL binding licensed an old admitted source", err) }
			if action == "owner_acl_denied" {
				enginetest.SessionSQL(t, writer, "REVOKE SELECT ON mv_metadata_target FROM mv_metadata_reader")
				beforeCatalog, generation, maintenance := e.Catalog.State, current.Generation, current.Maintenance
				readError := func(query string) error {
					op, err := reader.Acquire(context.Background())
					if err != nil { t.Fatal(err) }
					defer op.Done()
					result, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
					if result.Rows != nil { result.Rows.Close() }
					return err
				}
				enginetest.State(t, readError("SELECT id FROM public."+name), "42501")
				enginetest.State(t, readError("SELECT id FROM mv_metadata_anchor"), "25P02")
				if e.Catalog.State != beforeCatalog || e.Storage.Relations[ref.Address] != current || current.Generation != generation || current.Maintenance != maintenance || e.Storage.IndexRoots[indexRef.Address] != indexRoot { t.Fatal("current ACL denial changed committed metadata/roots") }
			} else {
				enginetest.TransactionRows(t, reader, "SELECT id FROM public."+name+" ORDER BY id", 1, 2).Close()
				retained := reader.Transactions.Roots[ref.Address]
				if reader.Transactions.Frontier != frontier || retained.Storage != root.Storage || retained.Relation != root.Relation || !storage.SameVersionRoot(retained.Versions, root.Versions) || retained.Ends != root.Ends { t.Fatal("new SQL read replaced actual retained owner roots") }
			}
			enginetest.TransactionRows(t, writer, "SELECT id FROM public."+name+" ORDER BY id", 2, 3).Close()
			if !reflect.DeepEqual(root.Versions, oldRows) || !reflect.DeepEqual(*root.Relation, oldDescriptor) || !storage.SameVersionRoot(root.Storage.Versions, root.Versions) || root.Storage.Ends != root.Ends { t.Fatal("metadata/refresh mutated independently owned old roots/payloads/descriptor") }
			enginetest.SessionSQL(t, reader, "ROLLBACK")
			enginetest.SessionSQL(t, reader, "RESET ROLE")
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestMaterializedRefreshConcurrentLateCancellation(t *testing.T) {
	e := engine.New()
	d := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.Catalog.State.MustRelation(d.Address())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := refreshUnitScope(t, e, ctx)
	stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Concurrent: parser.Position{Character: 27}}
	if err := core.ScopeAcquireMaterializedRefresh(scope, stmt); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	_, err = viewcmds.RefreshMaterializedLocked(e, scope, auth, stmt, 0)
	if err != nil {
		t.Fatal(err)
	}
	frontier := e.Visibility.CommitSeq()
	published := e.Storage.Relations[d.Address()]
	if !scope.Completed || len(published.Versions) != 1 {
		t.Fatal("actual refresh did not complete publication")
	}
	cancel()
	if err := scope.Finish(nil); err != nil {
		t.Fatal("late cancellation retracted completed utility", err)
	}
	if e.Storage.Relations[d.Address()] != published || e.Visibility.CommitSeq() != frontier {
		t.Fatal("late completion published twice")
	}
}
