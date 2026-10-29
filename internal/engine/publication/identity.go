package publication

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Actions describes statement capabilities, not the actions taken by its rows.
type Actions struct{ Update, Delete bool }

// CheckReplicaIdentity admits local UPDATE/DELETE execution against current
// publication definitions. It never evaluates a filter or changes local rows.
// The caller supplies the physical leaf, even for a root-bound publication.
func CheckReplicaIdentity(v catalog.Read, leaf *catalog.Relation, actions Actions) error {
	if !actions.Update && !actions.Delete || !IsPublishable(v, leaf) {
		return nil
	}
	mode := leaf.EffectiveReplicaIdentity()
	key := func(sub int32) bool {
		switch mode {
		case parser.ReplicaIdentityFull:
			return leaf.HasAttribute(sub)
		case parser.ReplicaIdentityIndex:
			return catalog.ReplicaIdentityKey(v, leaf, sub)
		case parser.ReplicaIdentityDefault:
			for _, constraint := range leaf.Constraints.Unique {
				if constraint.Primary && slices.Contains(constraint.Attributes, sub) {
					return true
				}
			}
		}
		return false
	}
	// Accumulate independently across publications before choosing a diagnostic:
	// a permissive publication cannot erase another one's invalid definition.
	var invalidFilter, invalidList, invalidGenerated Actions
	var published Actions
	mark := func(dst *Actions, relevant Actions) {
		dst.Update = dst.Update || relevant.Update
		dst.Delete = dst.Delete || relevant.Delete
	}
	var filterErr error
	err := VisitRelation(v, leaf, func(item EffectiveTable) bool {
		relevant := Actions{Update: actions.Update && item.Publication.Update, Delete: actions.Delete && item.Publication.Delete}
		if !relevant.Update && !relevant.Delete {
			return true
		}
		mark(&published, relevant)
		if mode != parser.ReplicaIdentityFull && item.Filter != nil {
			bad := false
			var inspect func(expr.Expr)
			inspect = func(root expr.Expr) {
				plan.WalkExpression(root, func(node expr.Expr) {
					column, ok := node.(plan.ColumnExpr)
					if !ok || bad {
						return
					}
					// The wrapper identifies the virtual output, not a stored
					// identity column. Its ordinary bound recipe retains the
					// generated owner's underlying stable column references.
					if column.Virtual != nil {
						inspect(column.Virtual)
						return
					}
					address := column.Origin.Address
					if address.SubID == 0 {
						return
					}
					whole := address
					whole.SubID = 0
					if whole != item.Source.Address() || !key(leafAttribute(item.Source, leaf, address.SubID)) {
						bad = true
					}
				})
			}
			if visitErr := item.Filter.VisitExpression(func(root expr.Expr) error { inspect(root); return nil }); visitErr != nil {
				filterErr = visitErr
				return false
			}
			if bad {
				mark(&invalidFilter, relevant)
			}
		}
		if item.ColumnsSpecified {
			bad := mode == parser.ReplicaIdentityFull
			for _, sub := range leaf.Attributes {
				if key(sub) && !slices.Contains(item.Columns, leafAttribute(leaf, item.Source, sub)) {
					bad = true
				}
			}
			if bad {
				mark(&invalidList, relevant)
			}
		} else {
			for sub, generation := range leaf.ColumnMeta.Generations {
				unpublished := generation.Kind == catalog.GenerationVirtual ||
					generation.Kind == catalog.GenerationStored && item.Publication.GeneratedColumns != 's'
				if unpublished && key(sub) {
					mark(&invalidGenerated, relevant)
				}
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	if filterErr != nil {
		return filterErr
	}
	for i, invalid := range [...]Actions{invalidFilter, invalidList, invalidGenerated} {
		if invalid.Update || invalid.Delete {
			detail := [...]string{
				"Column used in the publication WHERE expression is not part of the replica identity.",
				"Column list used by the publication does not cover the replica identity.",
				"Replica identity must not contain unpublished generated columns.",
			}[i]
			return identityError(leaf, invalid.Update, detail, false)
		}
	}
	if mode == parser.ReplicaIdentityFull {
		return nil
	}
	for _, sub := range leaf.Attributes {
		if key(sub) {
			return nil
		}
	}
	if published.Update || published.Delete {
		return identityError(leaf, published.Update, "", true)
	}
	return nil
}

// Stable attributes belong to their source layout. Partition attachment allows
// different attribute numbers and physical ordering, so bridge by current name.
func leafAttribute(source, target *catalog.Relation, sub int32) int32 {
	if source.Address() == target.Address() {
		return sub
	}
	for i, attr := range source.Attributes {
		if attr != sub {
			continue
		}
		for j, column := range target.Columns {
			if column.Name == source.Columns[i].Name {
				return target.Attributes[j]
			}
		}
		break
	}
	return 0
}

func identityError(leaf *catalog.Relation, update bool, detail string, missing bool) error {
	verb, gerund, action := "delete from table", "deleting from", "deletes"
	if update {
		verb, gerund, action = "update table", "updating", "updates"
	}
	e := failure("42P10", "cannot "+verb+` "`+leaf.Name()+`"`)
	e.Detail = detail
	if missing {
		e.Code = "55000"
		e.Message += " because it does not have a replica identity and publishes " + action
		e.Hint = "To enable " + gerund + " the table, set REPLICA IDENTITY using ALTER TABLE."
	}
	return e
}
