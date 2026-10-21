package index

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func KeyUsage(parent *catalog.Relation, key catalog.IndexKey) (sqltype.Usage, error) {
	usage, err := KeyBaseUsage(parent, key)
	if err != nil {
		return sqltype.Usage{}, err
	}
	return KeyCollation(usage, key), nil
}

func KeyBaseUsage(parent *catalog.Relation, key catalog.IndexKey) (sqltype.Usage, error) {
	if key.Derived != nil {
		return key.Derived.Execution.ResultUsage(), nil
	}
	for i, a := range parent.Attributes {
		if a == key.Attribute {
			return parent.Columns[i].Usage, nil
		}
	}
	return sqltype.Usage{}, fmt.Errorf("stuntgres: index attribute is missing")
}

func ExpressionDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	if d.SQLIndex == nil {
		return false
	}
	for _, key := range d.SQLIndex.Keys {
		if key.Derived == nil {
			continue
		}
		for _, ref := range key.Derived.Dependencies {
			a := ref.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}
