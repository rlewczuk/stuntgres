package partition

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// deparseBound uses the tree's captured descriptor roots, not the current
// relation's membership. The OID argument retains pg_get_expr's relation lookup
// semantics; replacing that relation cannot retarget an already captured tree.
func deparseBound(c *exec.Context, in table.DeparseInputs, oid sqltype.Value, result *sqltype.Descriptor, bound *catalog.PartitionMembership, keys []catalog.PartitionKeyEntry) (sqltype.Value, error) {
	if result == nil || result.ID() != sqltype.Text || in.Catalog.State() == nil || bound == nil {
		return sqltype.Value{}, invalidDeparse()
	}
	id, err := oid.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	if id != 0 {
		if _, ok := in.Catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)}); !ok {
			return sqltype.Null(result), nil
		}
	}
	var text string
	switch {
	case bound.DefaultBound:
		text = "DEFAULT"
	case bound.Strategy == parser.PartitionHash:
		text = fmt.Sprintf("FOR VALUES WITH (modulus %d, remainder %d)", bound.Modulus, bound.Remainder)
	case bound.Strategy == parser.PartitionList:
		if len(keys) != 1 {
			return sqltype.Value{}, invalidDeparse()
		}
		text, err = listBoundSQL(c.Context, in.Catalog, &keys[0], bound)
		text = strings.TrimPrefix(text, " ")
	case bound.Strategy == parser.PartitionRange:
		if len(keys) == 0 || len(keys) != len(bound.Lower) || len(keys) != len(bound.Upper) {
			return sqltype.Value{}, invalidDeparse()
		}
		var lower, upper string
		lower, err = tupleDumpSQL(c.Context, in.Catalog, keys, bound.Lower)
		if err == nil {
			upper, err = tupleDumpSQL(c.Context, in.Catalog, keys, bound.Upper)
		}
		text = "FOR VALUES FROM (" + lower + ") TO (" + upper + ")"
	default:
		return sqltype.Value{}, invalidDeparse()
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text), nil
}
