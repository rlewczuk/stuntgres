package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"testing"
)

// Retained TEMP descriptors pin target INT8/BOOL builtin identities. Imported
// column usages and every immutable stored-default node must agree with those
// identities without changing the source definition or evaluating defaults.
func TestSequenceRetainedTempDefaultReplacement(t *testing.T) {
	for _, mode := range []string{"ReplaceCatalog", "Restore"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TEMP SEQUENCE kept START 30")
			sequenceScalar(t, s, "SELECT nextval('kept')", 30)
			_, description := sequencePreparedDescription(t, e, s, "SELECT nextval('kept')")
			tempRef := description.sequences.Constants[0].Ref
			temp := e.catalog.State.Get(tempRef.Address)
			const script = "CREATE TABLE imported(n BIGINT DEFAULT CAST(7 AS BIGINT),b BOOL DEFAULT (CAST(1 AS BIGINT)=CAST(1 AS BIGINT)))"
			var source *Engine
			var sourceDefinition *catalog.Relation
			if mode == "Restore" {
				if err := e.Restore(context.Background(), strings.NewReader(script)); err != nil {
					t.Fatal(err)
				}
			} else {
				source = New()
				peer := sessionStart(t, source, nil)
				sessionSQL(t, peer, script)
				ref := catalogRef(t, source, source.CatalogRead(), "imported")
				sourceDefinition = source.catalog.State.MustRelation(ref.Address)
				if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			view := e.CatalogRead()
			ref := catalogRef(t, e, view, "imported")
			relation := view.MustRelation(ref.Address)
			for slot, attribute := range relation.Attributes {
				definition := relation.ColumnMeta.Defaults[attribute]
				if definition.Expression.ResultUsage() != relation.Columns[slot].Usage {
					t.Fatalf("default/column builtin identity mismatch: column %s", relation.Columns[slot].Name)
				}
				if canonical, err := stored.DefaultSQL(context.Background(), view, definition.Expression); err != nil || canonical == "" {
					t.Fatalf("default descendant identity or binding mismatch: column %s: %q %v", relation.Columns[slot].Name, canonical, err)
				}
			}
			if source != nil {
				ref := catalogRef(t, source, source.CatalogRead(), "imported")
				if source.catalog.State.Get(ref.Address) != sourceDefinition {
					t.Fatal("normalization mutated source descriptor")
				}
				for _, definition := range sourceDefinition.ColumnMeta.Defaults {
					if _, err := stored.DefaultSQL(context.Background(), source.CatalogRead(), definition.Expression); err != nil {
						t.Fatal("normalization mutated source default", err)
					}
				}
			}
			if e.catalog.State.Get(tempRef.Address) != temp {
				t.Fatal("normalization replaced retained TEMP descriptor")
			}
			if result := sessionSQL(t, s, "INSERT INTO imported DEFAULT VALUES"); result.Count != 1 {
				t.Fatal("default INSERT count", result.Count)
			}
			result := sessionSQL(t, s, "SELECT n,b FROM imported")
			rows := tuples(result)
			result.Rows.Close()
			if len(rows) != 1 || rows[0][0] != int64(7) || rows[0][1] != true {
				t.Fatal("normalized defaults", rows)
			}
			sequenceScalar(t, s, "SELECT nextval('kept')", 31)
		})
	}
}
