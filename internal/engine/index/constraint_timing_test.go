package index

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestUniqueTimingDefinitionInvariant(t *testing.T) {
	parent := &catalog.Relation{Constraints: catalog.RelationConstraints{Unique: []catalog.UniqueConstraint{{InitiallyDeferred: true}}}}
	if err := ValidateUniques(catalog.Read{}, parent, map[string]bool{}); err == nil || err.Error() != "stuntgres: invalid unique constraint timing" {
		t.Fatal("malformed timing accepted", err)
	}
}
