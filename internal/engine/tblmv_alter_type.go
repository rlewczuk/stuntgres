package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

type alterTypePreparation struct {
	usage              sqltype.Usage
	transformation     index.ColumnTransformation
	declaredDimensions int16
}

// Phase-one lookup uses the statement-original descriptor, before ADD/DROP passes.
func alterTypeColumn(original *catalog.Relation, name string) (int, error) {
	slot, ok := original.Index[name]
	if !ok {
		return 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, original.Name()))
	}
	return slot, nil
}
