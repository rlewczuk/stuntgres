package ddl

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

func TestAlterTablespaceKind(t *testing.T) {
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation, catalog.IndexRelation, catalog.MaterializedViewRelation} {
		if err := AlterTablespaceKind(&catalog.Relation{RelationKind: kind}); err != nil {
			t.Fatal(kind, err)
		}
	}
	for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.SequenceRelation, catalog.ForeignRelation} {
		err := AlterTablespaceKind(&catalog.Relation{ObjectName: "t", RelationKind: kind})
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42809" || got.Detail == "" {
			t.Fatal(kind, err)
		}
	}
}
