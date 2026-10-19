package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestTableLikeDefinitionDependencyOrder(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE anchor(a INT)")
	run(t, e, "CREATE VIEW prior_view AS SELECT a FROM anchor")
	// Ordinary CREATE is intentional: the common dependency formatter must not
	// special-case LIKE copies. CHECK declaration order is not alphabetical.
	run(t, e, "CREATE TABLE ordinary(a INT,r BOOL GENERATED ALWAYS AS(a>0 AND ('anchor'::regclass IS NOT NULL)) STORED,CONSTRAINT c2 CHECK(a>0 AND ('anchor'::regclass IS NOT NULL)),CONSTRAINT c1 CHECK(a>0 AND ('anchor'::regclass IS NOT NULL)))")
	run(t, e, "CREATE INDEX later_index ON ordinary(a) WHERE ('anchor'::regclass IS NOT NULL) AND a>0")
	table := e.CatalogRead().MustRelation(roleTableRef(t, e, "ordinary").Address)
	var first, second catalog.CheckConstraint
	for _, check := range table.Constraints.Check {
		if check.Name == "c2" {
			first = check
		}
		if check.Name == "c1" {
			second = check
		}
	}
	if first.Ordinal >= second.Ordinal || first.OID <= second.OID {
		t.Fatal("fixture must distinguish CHECK declaration and definition-identity ordering", first, second)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{roleTableRef(t, e, "anchor").Address})
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, edge := range edges {
		if !edge.automatic {
			labels = append(labels, edge.label)
		}
	}
	expected := []string{"view prior_view", "column r of table ordinary", "constraint c2 on table ordinary", "constraint c1 on table ordinary", "index later_index"}
	if !slices.Equal(labels, expected) {
		t.Fatal("mixed definition ordering changed", labels, expected)
	}
}
