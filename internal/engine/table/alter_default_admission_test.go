package table

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAlterDefaultActionKindAdmission(t *testing.T) {
	for _, kind := range []parser.AlterTableActionKind{parser.SetColumnDefault, parser.DropColumnDefault} {
		for _, accepted := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation, catalog.ForeignRelation} {
			if err := AlterTableActionKind(&catalog.Relation{RelationKind: accepted, ObjectName: "t"}, kind); err != nil {
				t.Fatalf("kind %d on %v: %v", kind, accepted, err)
			}
		}
		for _, tc := range []struct {
			name     string
			relation catalog.RelationKind
			object   string
			code     string
			message  string
			detail   string
		}{
			{"view", catalog.ViewRelation, "v", "0A000", "view mutation is unsupported", ""},
			{"materialized_view", catalog.MaterializedViewRelation, "mv", "42809", `cannot ALTER COLUMN ... SET DEFAULT relation "mv"`, "This operation is not supported for materialized views."},
			{"sequence", catalog.SequenceRelation, "s", "42809", `ALTER action ALTER COLUMN ... SET DEFAULT cannot be performed on relation "s"`, "This operation is not supported for sequences."},
			{"index", catalog.IndexRelation, "i", "42809", `cannot ALTER COLUMN ... SET DEFAULT relation "i"`, "This operation is not supported for indexes."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := AlterTableActionKind(&catalog.Relation{RelationKind: tc.relation, ObjectName: tc.object}, kind)
				diagnostic, ok := err.(*sqlstate.Error)
				if !ok || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Detail != tc.detail || diagnostic.Position != "" {
					t.Fatalf("admission = %#v, want %s %q %q", err, tc.code, tc.message, tc.detail)
				}
			})
		}
	}
}
