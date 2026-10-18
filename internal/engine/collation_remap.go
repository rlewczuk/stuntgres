package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type collationRemapper func(sqltype.CollationRef) (sqltype.CollationRef, error)

func remappedCollationCapabilities(next *catalogState, mapping map[ids.ObjectAddress]ids.ObjectAddress) collationRemapper {
	needed := false
	for from, to := range mapping {
		if from.ClassID == ids.CollationClassID && from != to {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	return func(old sqltype.CollationRef) (sqltype.CollationRef, error) {
		from := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(old.ID)}
		to, ok := mapping[from]
		if !ok {
			return old, nil
		}
		d, ok := next.descriptors[to].(*collationDescriptor)
		if !ok {
			d, ok = next.descriptors[from].(*collationDescriptor)
		}
		if !ok || !old.Valid() {
			return sqltype.CollationRef{}, fmt.Errorf("stuntgres: missing imported collation capability")
		}
		mapped, err := sqltype.RegisteredCollation(uint32(to.ObjectID), d.semantics)
		if err != nil {
			return sqltype.CollationRef{}, err
		}
		if mapped.CaseMode() != old.CaseMode() {
			return sqltype.CollationRef{}, fmt.Errorf("stuntgres: incompatible imported collation provider")
		}
		return mapped, nil
	}
}
