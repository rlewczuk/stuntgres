package tablecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestMaterializedStoragePublication(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "cancel"}[cancelled], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE ms_source(id int,note text)", "INSERT INTO ms_source VALUES(1,NULL),(1,NULL),(2,'two')", "CREATE MATERIALIZED VIEW ms_owned AS SELECT id,note FROM ms_source", "CREATE INDEX ms_owned_idx ON ms_owned(id)"} {
				enginetest.SessionSQL(t, s, q)
			}
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "ms_owned")
			indexRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "ms_owned_idx")
			old := e.CatalogRead().MustRelation(ref.Address)
			oldIndex := e.CatalogRead().MustRelation(indexRef.Address)
			root := e.Catalog.State
			store := e.Storage.Relations[ref.Address]
			metadata := e.Storage.RelationMetadata[ref.Address]
			rows, ends, generation := store.Versions, store.Ends, store.Generation
			indexRoot := e.Storage.IndexRoots[indexRef.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished materialized storage unit"))
			statement := &parser.AlterTable{Materialized: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ms_owned"}}}, Actions: []parser.AlterTableAction{
				{Action: parser.SetColumnStatistics, Column: parser.Identifier{Text: "id"}, StatisticsTarget: 7},
				{Action: parser.SetColumnOptions, Column: parser.Identifier{Text: "id"}, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "n_distinct"}, HasValue: true, Value: parser.Value{Text: "2"}}}},
				{Action: parser.SetStorage, Column: parser.Identifier{Text: "note"}, Property: parser.Identifier{Text: "external"}},
				{Action: parser.SetCompression, Column: parser.Identifier{Text: "note"}, Property: parser.Identifier{Text: "pglz"}},
				{Action: parser.SetRelationOptions, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "fillfactor"}, HasValue: true, Value: parser.Value{Text: "70"}}}},
				{Action: parser.ClusterOn, ClusterIndex: parser.Identifier{Text: "ms_owned_idx"}},
			}}
			if _, err := scope.Execute(statement, 0, nil); err != nil {
				t.Fatal(err)
			}
			candidate := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
			candidateIndex := scope.Catalog.Descriptors[indexRef.Address].(*catalog.Relation)
			if policy := candidate.ColumnStorage(1); policy.Storage != "e" || policy.Compression != "p" {
				t.Fatal("mixed storage/compression intent missing", policy)
			}
			if candidate == old || candidate.Revision() <= old.Revision() || candidate.ColumnMeta.Statistics[1].Target != 7 || candidateIndex == oldIndex || !candidateIndex.SQLIndex.Clustered || oldIndex.SQLIndex.Clustered || e.Catalog.State != root || len(old.ColumnMeta.Statistics) != 0 {
				t.Fatal("mixed candidate mutated published originals before Finish")
			}
			if cancelled {
				cancel()
			}
			err = scope.Finish(nil)
			if cancelled {
				if !errors.Is(err, context.Canceled) || e.Catalog.State != root || e.CatalogRead().MustRelation(ref.Address) != old || e.CatalogRead().MustRelation(indexRef.Address) != oldIndex {
					t.Fatal("canceled mixed stage published", err)
				}
			} else if err != nil || e.CatalogRead().MustRelation(ref.Address) != candidate || e.CatalogRead().MustRelation(indexRef.Address) != candidateIndex {
				t.Fatal("mixed stage failed sole publication", err)
			}
			if e.Storage.Relations[ref.Address] != store || e.Storage.RelationMetadata[ref.Address] != metadata || !storage.SameVersionRoot(rows, store.Versions) || store.Ends != ends || store.Generation != generation || e.Storage.IndexRoots[indexRef.Address] != indexRoot || root.MustRelation(ref.Address) != old || root.MustRelation(indexRef.Address) != oldIndex {
				t.Fatal("logical metadata changed retained store/index/version backing")
			}
		})
	}
	for _, materialized := range []bool{false, true} {
		for _, optionsFirst := range []bool{false, true} {
			name := map[bool]string{false: "table_spelling", true: "materialized_spelling"}[materialized] + "/" + map[bool]string{false: "statistics_first", true: "options_first"}[optionsFirst]
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE MATERIALIZED VIEW ms_order AS SELECT 1 AS id")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "ms_order")
				old := e.CatalogRead().MustRelation(ref.Address)
				root := e.Catalog.State
				store := e.Storage.Relations[ref.Address]
				rows, ends, generation := store.Versions, store.Ends, store.Generation
				statistics := parser.AlterTableAction{Action: parser.SetColumnStatistics, Column: parser.Identifier{Text: "id"}, StatisticsTarget: -2}
				options := parser.AlterTableAction{Action: parser.SetRelationOptions, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "bogus"}, HasValue: true, Value: parser.Value{Text: "1"}}}}
				actions := []parser.AlterTableAction{statistics, options}
				message := "statistics target -2 is too low"
				if optionsFirst {
					actions[0], actions[1] = options, statistics
					message = `unrecognized parameter "bogus"`
				}
				op, cleanup, err := enginetest.Operation(e, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished materialized order unit"))
				_, err = scope.Execute(&parser.AlterTable{Materialized: materialized, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ms_order"}}}, Actions: actions}, 0, nil)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != message {
					t.Fatal("actual-kind misc validation order", err)
				}
				if err := scope.Finish(err); err == nil {
					t.Fatal("failed statement completed successfully")
				}
				if e.Catalog.State != root || e.CatalogRead().MustRelation(ref.Address) != old || e.Storage.Relations[ref.Address] != store || !storage.SameVersionRoot(rows, store.Versions) || store.Ends != ends || store.Generation != generation {
					t.Fatal("failed validation order published or copied retained backing")
				}
			})
		}
	}
}
