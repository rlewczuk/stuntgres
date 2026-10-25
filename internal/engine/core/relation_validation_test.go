package core_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
)

// The validators of packages above table run at fixed points of relation
// validation: Policies, then Partition, then the view Definition tail. The
// first failing validator's error is returned and later ones do not run.
func TestRelationValidatorsOrder(t *testing.T) {
	e := engine.New()
	source, _ := namespaceUnitSource(t, e, e.CatalogRead())
	materialized := refreshUnitRelation(t, e)
	read := e.CatalogRead()
	for _, d := range []*catalog.Relation{source, materialized} {
		if err := core.RelationProvider.Validate(read, d); err != nil {
			t.Fatal(d.Name(), err)
		}
	}
	recording := func(calls *[]string, failing string, failure error) table.RelationProvider {
		hook := func(name string) table.RelationValidator {
			return func(catalog.Read, *catalog.Relation) error {
				*calls = append(*calls, name)
				if name == failing {
					return failure
				}
				return nil
			}
		}
		return table.RelationProvider{Validators: table.RelationValidators{Policies: hook("policies"), Partition: hook("partition"), Definition: hook("definition")}}
	}
	for _, c := range []struct {
		d    *catalog.Relation
		want []string
	}{
		{source, []string{"policies", "partition"}},
		{materialized, []string{"policies", "partition", "definition"}},
	} {
		var calls []string
		if err := recording(&calls, "", nil).Validate(read, c.d); err != nil || !slices.Equal(calls, c.want) {
			t.Fatal(c.d.Name(), calls, err)
		}
		for i, failing := range c.want {
			failure := errors.New("rejected by " + failing)
			calls = nil
			if err := recording(&calls, failing, failure).Validate(read, c.d); err != failure || !slices.Equal(calls, c.want[:i+1]) {
				t.Fatal(c.d.Name(), failing, calls, err)
			}
		}
	}
}
