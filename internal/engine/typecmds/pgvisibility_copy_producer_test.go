package typecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPgvisibilityCheckTypedMutationProducer(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserving_codec_copy", true: "destructive_rewrite"}[rewrite], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE EXTENSION pg_visibility", "CREATE TYPE visibility_typed_mood AS ENUM ('old','other')", "CREATE TABLE visibility_typed_heap(n visibility_typed_mood)", "INSERT INTO visibility_typed_heap VALUES('old')", "VACUUM (FREEZE) visibility_typed_heap", "SELECT * FROM pg_visibility_map_summary('visibility_typed_heap'::regclass)"} {
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
			ref := enginetest.CatalogRef(t, e, base, "visibility_typed_heap")
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
			raw := enginetest.Statement(t, "ALTER TABLE visibility_typed_heap ALTER COLUMN n TYPE visibility_typed_mood").(*parser.AlterTable)
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
			metadata := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(metadata)
			if err := metadata.Replace(&fresh); err != nil {
				t.Fatal(err)
			}
			// Real consumed metadata defers row ownership until StageTypedRows.
			phase, err := e.PublishAlterMetadata(scope, metadata, map[ids.ObjectAddress]bool{ref.Address: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			next := phase.View.MustRelation(ref.Address)
			if next == old || next.Columns[0].Usage.Type != codec || store.Versions[0].Values[0].Type() != oldType.SQLType || store.Visibility != root {
				t.Fatal("metadata did not defer the actual old-codec heap")
			}
			e.Mu.Lock()
			auth, err = e.AuthorizationLocked(op)
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, s)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			columnRef := ref
			columnRef.Address.SubID = old.Attributes[0]
			var transformation expr.Expr = plan.ColumnExpr{Info: expr.Info{Usage: old.Columns[0].Usage}, Origin: columnRef, Scope: 1, Frame: 1, Slot: 0}
			if rewrite {
				value, err := codec.Input(t.Context(), "renamed", -1)
				if err != nil {
					t.Fatal(err)
				}
				transformation = plan.LiteralExpr{Info: expr.Info{Usage: codec.Usage()}, Value: value}
			}
			mutation := typecmds.TypedRelationMutation{Old: old, Next: next, Transforms: []index.ColumnTransformation{{Attribute: old.Attributes[0], Expression: transformation, Rewrite: rewrite}}}
			if err := typecmds.StageTypedRows(e, scope, auth, stage, mutation); err != nil {
				t.Fatal(err)
			}
			shape, ok := stage.RowShapes[ref.Address]
			if !ok || shape.Original != store || shape.Replacement == store {
				t.Fatal("actual typed producer did not build private storage", shape)
			}
			if rewrite {
				if shape.PreservesVisibility || !shape.Rewritten || shape.Replacement.Visibility != nil || shape.Replacement.Generation <= generation {
					t.Fatal("typed destructive producer retained VM authority", shape)
				}
			} else {
				copy := shape.Replacement
				if !shape.PreservesVisibility || shape.Rewritten || shape.ObservedVisibility != root || copy.Visibility != root || copy.Generation != generation || copy.Ends != ends || copy.Headers != headers || copy.Versions[0].Version != versions[0].Version || copy.Versions[0].Born != versions[0].Born || copy.Versions[0].Values[0].Type() != codec {
					t.Fatal("real ReownVersions copy failed coherent VM provenance", shape)
				}
			}
			if e.Storage.Relations[ref.Address] != store || store.Visibility != root || !storage.SameVersionRoot(store.Versions, versions) || store.Ends != ends || store.Headers != headers || store.Generation != generation || store.Versions[0].Values[0].Type() != oldType.SQLType || root.Invalidated {
				t.Fatal("typed producer mutated source")
			}
			_ = scope.Finish(context.Canceled)
			if e.CatalogRead().State() != base.State() || e.Storage.Relations[ref.Address] != store || store.Visibility != root {
				t.Fatal("discarded typed work installed a prefix")
			}
		})
	}
}
