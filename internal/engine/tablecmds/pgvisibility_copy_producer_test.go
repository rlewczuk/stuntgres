package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPgvisibilityCheckAlterPhysicalProducer(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserving_codec_copy", true: "destructive_rewrite"}[rewrite], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE EXTENSION pg_visibility", "CREATE TYPE visibility_alter_mood AS ENUM ('old','other')", "CREATE TABLE visibility_alter_heap(n visibility_alter_mood)", "INSERT INTO visibility_alter_heap VALUES('old')", "VACUUM (FREEZE) visibility_alter_heap", "SELECT * FROM pg_visibility_map_summary('visibility_alter_heap'::regclass)"} {
				result := enginetest.SessionSQL(t, s, q)
				if result.Rows != nil {
					values := make([]sqltype.Value, len(result.Rows.Schema()))
					for {
						present, err := result.Rows.Next(t.Context(), values)
						if err != nil {
							t.Fatal(err)
						}
						if !present {
							break
						}
					}
					if err := result.Rows.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			base := e.CatalogRead()
			ref := enginetest.CatalogRef(t, e, base, "visibility_alter_heap")
			old := base.MustRelation(ref.Address)
			store := e.Storage.Relations[ref.Address]
			root := store.Visibility
			if root == nil || root.Invalidated || root.Map.AllVisible != 1 {
				t.Fatal("missing genuine marked source")
			}
			versions, ends, headers, generation := store.Versions, store.Ends, store.Headers, store.Generation
			oldType := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Columns[0].Usage.Type.ID())})
			members := oldType.SQLType.EnumMembers()
			members[0].Label = "renamed"
			codec, err := sqltype.EnumWithMembers(oldType.SQLType.ID(), oldType.Name(), oldType.SQLType.SQLName(), members)
			if err != nil {
				t.Fatal(err)
			}
			fresh := *oldType
			fresh.SQLType = codec
			fresh.ObjectRevision++
			enginetest.SessionSQL(t, s, "BEGIN")
			defer func() {
				if s.TransactionState() != session.TxIdle {
					enginetest.SessionSQL(t, s, "ROLLBACK")
				}
			}()
			op, err := s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.Finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			query := "ALTER TABLE visibility_alter_heap ALTER COLUMN n TYPE visibility_alter_mood"
			if rewrite {
				query = "ALTER TABLE visibility_alter_heap ALTER COLUMN n TYPE text USING n::text"
			}
			raw := enginetest.Statement(t, query).(*parser.AlterTable)
			if err := scope.Gate(raw); err != nil {
				t.Fatal(err)
			}
			if err := scope.AcquireRelation(raw, true); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.StatementAuthorizationLocked(op, raw)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			preparation, err := tablecmds.PrepareAlterTableActions(e, scope, op, auth, old, scope.LockedRef, raw.Actions)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			// Use the real hierarchy collector/consumed metadata boundary. The
			// fresh enum generation is metadata only until this exact producer.
			e.Mu.Lock()
			scope.Catalog = scope.PendingCatalog().Clone()
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, s)
			err = stage.Replace(&fresh)
			scope.Inheritance.Alter = &core.InheritanceAlterState{Stage: stage, InheritanceAlterPlan: table.InheritanceAlterPlan{Root: old, Actions: raw.Actions, Types: preparation.Types, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{}}}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { scope.Inheritance.Alter = nil; e.DiscardCatalogStage(stage) }()
			var physical []tablecmds.AlterTablePhysical
			e.Mu.Lock()
			_, err = tablecmds.AlterTableLocked(e, scope, op, auth, raw, 0, &physical, nil)
			if err != nil || len(physical) != 1 {
				t.Fatal("actual physical collector", len(physical), err)
			}
			phase, err := e.PublishAlterMetadata(scope, stage, map[ids.ObjectAddress]bool{ref.Address: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if e.Storage.Relations[ref.Address] != store || store.Visibility != root || store.Versions[0].Values[0].Type() != oldType.SQLType || !storage.SameVersionRoot(store.Versions, versions) {
				t.Fatal("metadata phase copied rows before producer")
			}
			if !rewrite && phase.View.MustRelation(ref.Address).Columns[0].Usage.Type != codec {
				t.Fatal("fresh descriptor generation was not consumed")
			}
			_, err = tablecmds.FinishAlterPhysical(e, scope, phase, &physical[0], stage.IndexesOrZero().Layouts)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.FinishAlterValidation(scope, phase); err != nil {
				t.Fatal(err)
			}
			change, ok := scope.Catalog.Storage[ref.Address]
			if !ok || change.Original != store || change.Replacement == store {
				t.Fatal("producer did not make actual private storage", change)
			}
			if rewrite {
				if change.PreservesVisibility || !change.Rewritten || change.Replacement.Visibility != nil || change.Replacement.Generation <= generation {
					t.Fatal("destructive producer borrowed VM authority", change)
				}
			} else {
				copy := change.Replacement
				if !change.PreservesVisibility || change.Rewritten || change.ObservedVisibility != root || copy.Visibility != root || copy.Generation != generation || copy.Ends != ends || copy.Headers != headers || copy.Versions[0].Version != versions[0].Version || copy.Versions[0].Born != versions[0].Born || copy.Versions[0].Values[0].Type() != codec {
					t.Fatal("actual ReownRelation seam did not mint coherent provenance", change)
				}
			}
			if e.Storage.Relations[ref.Address] != store || store.Visibility != root || !storage.SameVersionRoot(store.Versions, versions) || store.Ends != ends || store.Headers != headers || store.Generation != generation || root.Invalidated {
				t.Fatal("producer mutated committed source")
			}
			_ = scope.Finish(context.Canceled)
			if e.CatalogRead().State() != base.State() || e.Storage.Relations[ref.Address] != store || store.Visibility != root {
				t.Fatal("discarded producer published a prefix")
			}
		})
	}
}
