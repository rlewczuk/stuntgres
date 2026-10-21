package table

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// TYPE recreates affected NN identities without changing names or validity.
// It runs right after index.RebuildAlterUniques; with no changes it is a no-op.
// New attachments already describe the completed shape and are not recreated.
func RebuildAlterNotNulls(h ddl.Host, parent *catalog.Relation, changes []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(changes) == 0 {
		return parent, nil
	}
	original, ok := h.StageBase().Relation(parent.Address())
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	var next *catalog.Relation
	for i, n := range parent.Constraints.NotNull {
		incumbent := false
		for _, old := range original.Constraints.NotNull {
			if old.OID == n.OID {
				incumbent = true
				break
			}
		}
		if !incumbent {
			continue
		}
		affected := false
		for _, change := range changes {
			if change.Attribute == n.Attribute {
				affected = true
				break
			}
		}
		if !affected {
			continue
		}
		if next == nil {
			copy := *parent
			copy.Constraints.NotNull = slices.Clone(parent.Constraints.NotNull)
			next = &copy
		}
		oid, err := h.AllocateOID()
		if err != nil {
			return nil, err
		}
		next.Constraints.NotNull[i].OID = oid
	}
	if next == nil {
		return parent, nil
	}
	if err := RetireRebuiltNotNullHistory(parent, next); err != nil {
		return nil, err
	}
	if err := h.Stage().Replace(next); err != nil {
		return nil, err
	}
	return next, nil
}
