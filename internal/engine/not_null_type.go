package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// TYPE recreates affected NN identities without changing names or validity.
// New attachments already describe the completed shape and are not recreated.
func (e *Engine) rebuildAlterNotNullsLocked(stage *CatalogStage, parent *catalog.Relation, changes []columnTransformation) (*catalog.Relation, error) {
	original, ok := stage.Base.Relation(parent.Address())
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	var next *catalog.Relation
	for i, n := range parent.NotNullConstraints {
		incumbent := false
		for _, old := range original.NotNullConstraints {
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
			if change.attribute == n.Attribute {
				affected = true
				break
			}
		}
		if !affected {
			continue
		}
		if next == nil {
			copy := *parent
			copy.NotNullConstraints = slices.Clone(parent.NotNullConstraints)
			next = &copy
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		next.NotNullConstraints[i].OID = oid
	}
	if next == nil {
		return parent, nil
	}
	if err := retireRebuiltNotNullHistory(parent, next); err != nil {
		return nil, err
	}
	if err := stage.Replace(next); err != nil {
		return nil, err
	}
	return next, nil
}
