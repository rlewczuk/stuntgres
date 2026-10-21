package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func partitionGeneratedError(_ error, pos parser.Position, name string) error {
	d := expr.ErrorAt("42P17", "cannot use generated column in partition key", pos).(*sqlstate.Error)
	d.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, name)
	return d
}

// preparePartitionBounds checks both arities before any expression demand.
// Transformation remains deferred: a later bind error must not mask an earlier
// NULL or prevent a required nontransactional sequence draw.
func preparePartitionBounds(parent *catalog.Relation, raw parser.PartitionBound) (*catalog.PartitionMembership, error) {
	bound := &catalog.PartitionMembership{Parent: parent.Address(), Strategy: parent.Partition.Strategy, DefaultBound: raw.Default}
	if parent.Partition.Strategy == parser.PartitionHash {
		if raw.Default {
			return nil, sqlError("42P16", "a hash-partitioned table may not have a default partition")
		}
		if raw.Strategy != parser.PartitionHash {
			return nil, expr.ErrorAt("42P16", "invalid bound specification for a hash partition", raw.Pos)
		}
		if raw.Modulus <= 0 {
			return nil, sqlError("42P16", "modulus for hash partition must be an integer value greater than zero")
		}
		if raw.Remainder >= raw.Modulus {
			return nil, sqlError("42P16", "remainder for hash partition must be less than modulus")
		}
		bound.Modulus, bound.Remainder = raw.Modulus, raw.Remainder
		return bound, nil
	}
	if raw.Strategy == parser.PartitionHash {
		return nil, expr.ErrorAt("42P16", "invalid bound specification for a range partition", raw.Pos)
	}
	if raw.Default {
		return bound, nil
	}
	keys := parent.Partition.Keys
	if len(raw.Lower) != len(keys) {
		return nil, sqlError("42P16", "FROM must specify exactly one value per partitioning column")
	}
	if len(raw.Upper) != len(keys) {
		return nil, sqlError("42P16", "TO must specify exactly one value per partitioning column")
	}
	bound.Lower, bound.Upper = make([]catalog.PartitionDatum, len(keys)), make([]catalog.PartitionDatum, len(keys))
	return bound, nil
}
