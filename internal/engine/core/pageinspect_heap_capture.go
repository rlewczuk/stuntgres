package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// Called under Engine.Mu after the shared AccessShare wait/readmission. Only
// physical descriptor facts escape that lock; no rows or type codecs escape.
func pageinspectHeapColumns(relation *catalog.Relation) ([]sqltype.HeapStoredColumn, error) {
	if relation.RelationKind != catalog.SequenceRelation && relation.TableMethod != 2 && !(relation.RelationKind == catalog.CatalogRelation && !relation.CatalogView) {
		return nil, SQLError("0A000", "only heap AM is supported")
	}
	count := int(relation.ColumnMeta.HighWater)
	if count < len(relation.Columns) {
		count = len(relation.Columns)
	}
	for _, number := range relation.Attributes {
		if int(number) > count {
			count = int(number)
		}
	}
	columns := make([]sqltype.HeapStoredColumn, count)
	for number, dropped := range relation.ColumnMeta.Dropped {
		if number <= 0 || int(number) > count {
			return nil, fmt.Errorf("stuntgres: invalid dropped heap attribute")
		}
		column, err := sqltype.HeapColumnFacts(dropped.Facts)
		if err != nil {
			return nil, err
		}
		columns[number-1] = column
	}
	for i, live := range relation.Columns {
		number := i + 1
		if len(relation.Attributes) != 0 {
			number = int(relation.Attributes[i])
		}
		if number <= 0 || number > count || live.Usage.Type == nil {
			return nil, fmt.Errorf("stuntgres: invalid live heap attribute")
		}
		column, err := sqltype.HeapColumnFacts(live.Usage.Type.AttributeFacts())
		if err != nil {
			return nil, err
		}
		columns[number-1] = column
	}
	for _, column := range columns {
		if column.Alignment == 0 {
			return nil, fmt.Errorf("stuntgres: missing physical heap attribute")
		}
	}
	return columns, nil
}
