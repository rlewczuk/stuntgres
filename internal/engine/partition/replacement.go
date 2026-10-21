package partition

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func HasHashPartition(snapshot *catalog.State) bool {
	for _, d := range snapshot.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && relation.Partition != nil && relation.Partition.Strategy == parser.PartitionHash {
			return true
		}
	}
	return false
}

// Partition rows have equal named columns, but private replay may preserve a
// different physical order. Borrow values unless a real layout change is needed.
func ReplacementRow(from, to *catalog.Relation, row []sqltype.Value) []sqltype.Value {
	same := true
	for i, c := range to.Columns {
		if from.Columns[i].Name != c.Name {
			same = false
			break
		}
	}
	if same {
		return row
	}
	values := make([]sqltype.Value, len(to.Columns))
	for i, c := range to.Columns {
		values[i] = row[from.Index[c.Name]]
	}
	return values
}
