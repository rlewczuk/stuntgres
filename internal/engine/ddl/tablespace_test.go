package ddl

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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

// TablespaceDatabase hands its WARNING back instead of emitting it; an OID
// that names no tablespace reads nothing from the catalog.
func TestTablespaceDatabaseReturnsWarning(t *testing.T) {
	var oid *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Oid {
			oid = d
		}
	}
	value, present, warning, err := TablespaceDatabase(newFixture(t).read, 4294967295, oid)
	want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "4294967295 is not a tablespace OID"}
	if err != nil || present || !value.IsNull() || value.Type() != oid || warning == nil || *warning != want {
		t.Fatal(value, present, warning, err)
	}
}
