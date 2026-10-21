package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func viewDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	return d.View != nil && d.View.DependsOn(target)
}

type viewDependencyEdge struct {
	view   *catalog.Relation
	target ids.ObjectAddress
}

func validateViewDefinition(v catalog.Read, d *catalog.Relation) error {
	if d.View == nil {
		return fmt.Errorf("stuntgres: invalid view definition")
	}
	return d.View.Validate(v, d)
}
