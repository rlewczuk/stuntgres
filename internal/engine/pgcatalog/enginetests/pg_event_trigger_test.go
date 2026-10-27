package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestPGEventTriggerAttributeNullability(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.EventTriggerClassID}
	source := view.MustRelation(address)
	if len(source.Columns) != 7 || len(source.ColumnMeta.CatalogAttributes) != 7 {
		t.Fatalf("event-trigger attribute shape: columns=%d facts=%d, want seven each", len(source.Columns), len(source.ColumnMeta.CatalogAttributes))
	}
	names := []string{"oid", "evtname", "evtevent", "evtowner", "evtfoid", "evtenabled", "evttags"}
	for i, column := range source.Columns {
		if column.Name != names[i] || !column.NullabilityKnown || column.Nullable != (i == 6) {
			t.Fatalf("event-trigger column %d: name=%q nullabilityKnown=%t nullable=%t, want name=%q known=true nullable=%t", i, column.Name, column.NullabilityKnown, column.Nullable, names[i], i == 6)
		}
		fact := source.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull != (i < 6) {
			t.Fatalf("event-trigger initialized attribute %q: NotNull=%t, want %t", column.Name, fact.NotNull, i < 6)
		}
		metadata := pgcatalog.AttributeMetadata(source, i)
		if metadata.NotNull != (i < 6) {
			t.Fatalf("event-trigger exposed attribute %q: NotNull=%t, want %t", column.Name, metadata.NotNull, i < 6)
		}
		if i < 6 && metadata.Dimensions != 0 {
			t.Fatalf("event-trigger scalar attribute %q: dimensions=%d, want 0", column.Name, metadata.Dimensions)
		}
		if i == 6 && (column.Usage.Type.ID() != 1009 || metadata.TypeID != 1009 || metadata.Dimensions != 1) {
			t.Fatalf("event-trigger evttags: declared type=%d exposed type=%d dimensions=%d, want 1009/1009/1", column.Usage.Type.ID(), metadata.TypeID, metadata.Dimensions)
		}
	}
}
