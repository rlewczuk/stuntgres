package tablecmds_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
)

func TestVacuumFullMetadataRetention(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		owner, metadataFirst, denied bool
	}{
		{name: "full_then_rename"},
		{name: "rename_then_full", metadataFirst: true},
		{name: "full_then_owner", owner: true},
		{name: "owner_then_full", owner: true, metadataFirst: true},
		{name: "current_acl_denied_full_then_owner", owner: true, denied: true},
		{name: "current_acl_denied_owner_then_full", owner: true, metadataFirst: true, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			writer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_meta_target(id INT PRIMARY KEY)", "INSERT INTO full_meta_target VALUES(1),(2)"} {
				sessionSQL(t, writer, q)
			}
			if tc.owner {
				for _, q := range []string{"CREATE ROLE full_meta_owner", "CREATE ROLE full_meta_reader", "GRANT USAGE,CREATE ON SCHEMA public TO full_meta_owner", "GRANT USAGE ON SCHEMA public TO full_meta_reader", "REVOKE ALL ON full_mixed_anchor,full_meta_target FROM PUBLIC", "GRANT SELECT ON full_mixed_anchor,full_meta_target TO full_meta_reader"} {
					sessionSQL(t, writer, q)
				}
			}
			var readerRole []string
			if tc.owner {
				readerRole = []string{"full_meta_reader"}
			}
			pin := pinFullMixed(t, e, "full_meta_target", readerRole...)
			seedDescriptor := *pin.root.Relation
			seedRows := slices.Clone(pin.root.Versions)
			seedPayloads := slices.Clone(pin.root.Versions)
			for i := range seedRows {
				seedRows[i].Values = slices.Clone(seedRows[i].Values)
			}
			origin := pin.root.Relation.EffectiveStorageLocator(e.CatalogRead())
			sessionSQL(t, writer, "UPDATE full_meta_target SET id=3 WHERE id=1")
			// DML legitimately advances the current store's roots. Retain the
			// independently captured old snapshot vectors, while FULL ownership
			// assertions start from its actual post-DML predecessor.
			pin.owned = captureFullOwned(t, e, "full_meta_target")
			name := "full_meta_target"
			rewrite := func() {
				t.Helper()
				before := captureFullOwned(t, e, name)
				sessionSQL(t, writer, "VACUUM FULL "+name)
				next := before.replaced(t, e)
				if len(next.Versions) != 2 || next.Retained != before.predecessors {
					t.Fatal("guarded FULL lost current heads or genuine UPDATE history")
				}
				// An actual admitted leaf with unchanged defining metadata still
				// passes the strict cross-replacement source contract.
				leaf := exec.Leaf{Ref: before.ref, Relation: before.descriptor, Storage: before.store}
				if _, err := view.CurrentQuerySource(e.CatalogRead(), e.Catalog.Domain, before.ref.Epoch, next, leaf); err != nil {
					t.Fatal("strict equal-metadata source control", err)
				}
			}
			metadata := func() {
				t.Helper()
				before := captureFullOwned(t, e, name)
				want := *before.descriptor
				if tc.owner {
					sessionSQL(t, writer, "ALTER TABLE "+name+" OWNER TO full_meta_owner")
					target := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("full_meta_owner")
					if target.ID() == 0 || target == before.descriptor.Owner {
						t.Fatal("missing actual distinct owner")
					}
					want.Owner = target
					want.Security.ACL = authorization.SchemaACLOwner(before.descriptor.Security.ACL, before.descriptor.Owner, target)
				} else {
					sessionSQL(t, writer, "ALTER TABLE "+name+" RENAME TO full_meta_renamed")
					name = "full_meta_renamed"
					want.ObjectName = name
				}
				current := captureFullOwned(t, e, name)
				want.ObjectRevision = current.descriptor.Revision()
				if current.descriptor == before.descriptor || current.descriptor.Revision() <= before.descriptor.Revision() || !reflect.DeepEqual(current.descriptor, &want) || current.store != before.store || !maps.Equal(current.indexes, before.indexes) || current.ref != before.ref || !index.SameRelationRowShape(before.descriptor, current.descriptor) {
					t.Fatal("metadata changed more than its actual name/owner/revision/ACL authority")
				}
				for address := range before.indexes {
					if current.catalog.MustRelation(address).StorageLocator != before.catalog.MustRelation(address).StorageLocator {
						t.Fatal("metadata reminted an index locator")
					}
				}
				before.retained(t)
			}
			if tc.metadataFirst {
				metadata()
				rewrite()
			} else {
				rewrite()
				metadata()
			}
			current := captureFullOwned(t, e, name)
			if current.retainedOrigin != origin || current.truncateBoundary != 0 || pin.root.Storage.RetainedOrigin != 0 || pin.root.Storage.TruncateBoundary != 0 || !view.SameStableReadLineage(pin.root.Relation, current.descriptor, pin.root.Storage, current.store) || view.SameRetainedLineage(pin.root.Relation, current.descriptor, pin.root.Storage, current.store) || view.CrossedTruncate(pin.root.Relation, current.descriptor, pin.root.Storage, current.store) {
				t.Fatal("new-read versus captured-source lineage demands were conflated")
			}
			oldLeaf := exec.Leaf{Ref: pin.owned.ref, Relation: pin.root.Relation, Storage: pin.root.Storage}
			if _, err := view.CurrentQuerySource(e.CatalogRead(), e.Catalog.Domain, current.ref.Epoch, current.store, oldLeaf); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("new-read continuity licensed an obsolete captured source", err)
			}
			// Distinct but structurally equal layout backing is not a new-read
			// authority fence. Real metadata transitions above remain the proof.
			layout := *current.descriptor
			layout.Columns, layout.Attributes = slices.Clone(layout.Columns), slices.Clone(layout.Attributes)
			if !view.SameStableReadLineage(pin.root.Relation, &layout, pin.root.Storage, current.store) || view.SameRetainedLineage(pin.root.Relation, &layout, pin.root.Storage, current.store) {
				t.Fatal("new-read policy borrowed defining backing-array identity")
			}
			pin.retained(t)
			if tc.denied {
				sessionSQL(t, writer, "REVOKE SELECT ON full_meta_target FROM full_meta_reader")
				current = captureFullOwned(t, e, name)
				enginetest.State(t, fullMixedReadError(t, e, pin.reader, name), "42501")
				enginetest.State(t, fullMixedReadError(t, e, pin.reader, "full_mixed_anchor"), "25P02")
				current.rejected(t, e)
			} else {
				fullMixedRows(t, pin.reader, name, 1, 2)
			}
			fullMixedRows(t, writer, name, 2, 3)
			if tc.denied {
				// A failed block releases its live root owner, not the immutable
				// backing retained independently by this witness.
				pin.owned.retained(t)
				if pin.reader.Transactions.State != session.TxAborted || pin.reader.Transactions.Roots != nil || pin.reader.Transactions.Frontier != 0 {
					t.Fatal("denied new read retained failed-transaction snapshot authority")
				}
			} else {
				pin.retained(t)
			}
			if !reflect.DeepEqual(*pin.root.Relation, seedDescriptor) || !reflect.DeepEqual(pin.root.Versions, seedRows) {
				t.Fatal("metadata/FULL mutated original descriptor or snapshot headers/payloads")
			}
			for i, row := range pin.root.Versions {
				if len(row.Values) != 0 && &row.Values[0] != &seedPayloads[i].Values[0] {
					t.Fatal("FULL/metadata replaced retained payload backing")
				}
			}
			sessionSQL(t, pin.reader, "ROLLBACK")
			lockEmpty(t, e)
		})
	}

	t.Run("lineage_negative_demands", func(t *testing.T) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE full_negative(id INT)", "INSERT INTO full_negative VALUES(1)", "CREATE MATERIALIZED VIEW full_negative_definition AS SELECT id FROM full_negative"} {
			sessionSQL(t, s, q)
		}
		before := captureFullOwned(t, e, "full_negative")
		sessionSQL(t, s, "VACUUM FULL full_negative")
		after := captureFullOwned(t, e, "full_negative")
		definitionRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "full_negative_definition")
		definition := e.CatalogRead().MustRelation(definitionRef.Address).View
		if !view.SameStableReadLineage(before.descriptor, after.descriptor, before.store, after.store) {
			t.Fatal("missing genuine positive lineage control")
		}
		for _, negative := range []string{"object", "column_value", "attribute_value", "view_definition", "rowtype", "table_method", "kind", "family", "ungrounded", "newer_boundary", "reverse_boundary", "unpopulated_mv", "nil_relation", "nil_store"} {
			t.Run(negative, func(t *testing.T) {
				old, current := *before.descriptor, *after.descriptor
				a, b := *before.store, *after.store
				oldPointer, currentPointer, aPointer, bPointer := &old, &current, &a, &b
				switch negative {
				case "object":
					current.ObjectAddress.ObjectID++
				case "column_value":
					current.Columns = slices.Clone(current.Columns)
					current.Columns[0].Name = "different"
				case "attribute_value":
					current.Attributes = slices.Clone(current.Attributes)
					current.Attributes[0]++
				case "view_definition":
					current.View = definition
				case "rowtype":
					current.RowType++
				case "table_method":
					// A different existing AM identity, not new table-AM support.
					current.TableMethod = 403
				case "kind":
					current.RelationKind = catalog.ViewRelation
				case "family":
					b.RetainedOrigin++
				case "ungrounded":
					a.RetainedOrigin, b.RetainedOrigin = 0, 0
				case "newer_boundary":
					b.TruncateBoundary = 1
				case "reverse_boundary":
					a.TruncateBoundary = 2
					b.TruncateBoundary = 1
				case "unpopulated_mv":
					old.RelationKind, current.RelationKind = catalog.MaterializedViewRelation, catalog.MaterializedViewRelation
					old.Populated, current.Populated = true, false
				case "nil_relation":
					currentPointer = nil
				case "nil_store":
					bPointer = nil
				}
				if view.SameStableReadLineage(oldPointer, currentPointer, aPointer, bPointer) || view.SameRetainedLineage(oldPointer, currentPointer, aPointer, bPointer) {
					t.Fatal("new or captured stable read adopted inadmissible lineage", negative)
				}
			})
		}
		before.retained(t)
		after.retained(t)
		lockEmpty(t, e)
	})

	t.Run("actual_shape_replacement_rejects", func(t *testing.T) {
		e := engine.New()
		writer := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_shape(id INT)", "INSERT INTO full_shape VALUES(1),(2)"} {
			sessionSQL(t, writer, q)
		}
		pin := pinFullMixed(t, e, "full_shape")
		sessionSQL(t, writer, "VACUUM FULL full_shape")
		sessionSQL(t, writer, "ALTER TABLE full_shape ALTER COLUMN id TYPE BIGINT")
		pin.retained(t)
		if err := fullMixedReadError(t, e, pin.reader, "full_shape"); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("actual row-shape rewrite entered old read family", err)
		}
		pin.owned.retained(t)
		sessionSQL(t, pin.reader, "ROLLBACK")
		lockEmpty(t, e)
	})

	for _, boundary := range []string{"foreign_domain", "reset_epoch", "restore_epoch"} {
		t.Run(boundary, func(t *testing.T) {
			source := engine.New()
			writer := sessionStart(t, source, nil)
			for _, q := range []string{"CREATE TABLE full_boundary(id INT)", "INSERT INTO full_boundary VALUES(1)", "VACUUM FULL full_boundary"} {
				sessionSQL(t, writer, q)
			}
			before := captureFullOwned(t, source, "full_boundary")
			destination := source
			switch boundary {
			case "foreign_domain":
				destination = engine.New()
				if _, err := destination.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			case "reset_epoch":
				if err := engine.Reset(context.Background(), source); err != nil {
					t.Fatal(err)
				}
			case "restore_epoch":
				if err := engine.Restore(context.Background(), source, bytes.NewBufferString("CREATE TABLE full_boundary(id INT);INSERT INTO full_boundary VALUES(1);")); err != nil {
					t.Fatal(err)
				}
			}
			observer := sessionStart(t, destination, nil)
			op, err := observer.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := destination.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished lineage boundary witness"))
			result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			result.Rows.Close()
			existing := scope.View
			destination.Mu.Lock()
			captured, err := scope.CaptureDataViewsAtLocked(destination, []ids.ObjectRef{before.ref}, false, nil)
			destination.Mu.Unlock()
			if captured != nil || scope.View != existing || err == nil || boundary == "foreign_domain" && !strings.Contains(err.Error(), "foreign catalog reference") || boundary != "foreign_domain" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("canonical capture admitted wrong domain/epoch or changed owned view", err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			before.retained(t)
			lockEmpty(t, destination)
		})
	}
}
