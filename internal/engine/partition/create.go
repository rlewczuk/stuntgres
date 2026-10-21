package partition

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
)

func TupleSQL(ctx context.Context, tuple []catalog.PartitionDatum) (string, error) {
	var text strings.Builder
	for i, datum := range tuple {
		if i > 0 {
			text.WriteString(", ")
		}
		switch datum.Kind {
		case -1:
			text.WriteString("MINVALUE")
		case 1:
			text.WriteString("MAXVALUE")
		default:
			value, err := datum.Value.Type().SQLLiteral(ctx, datum.Value)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}

// Boundary is a statement-local projection of the one immutable child
// topology. Tuples and descriptors are referenced, not copied or persisted.
type Boundary struct {
	Tuple    []catalog.PartitionDatum
	Relation *catalog.Relation
	Lower    bool
}
