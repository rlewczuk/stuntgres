package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// insertConflictState is one command's ON CONFLICT arbitration: the bound
// clause it executes, the selected arbiters, unique-key checkpoints, the
// affected count, the EXCLUDED frame and the write overlay.
type insertConflictState struct {
	*analyze.InsertConflict
	selected      []ids.ObjectAddress
	catalog       *catalog.State
	checkpoint    lock.Checkpoint
	statementKeys lock.Checkpoint
	affected      int
	frame         []sqltype.Value
	insertWriteOverlay
}

// newInsertConflictState starts the arbitration state of one command over c,
// or returns nil without a clause.
func newInsertConflictState(c *analyze.InsertConflict) *insertConflictState {
	if c == nil {
		return nil
	}
	return &insertConflictState{InsertConflict: c}
}

func (c *insertConflictState) selectArbiters(ctx *exec.Context, parent *catalog.Relation, view catalog.Read) error {
	c.selected = c.selected[:0]
	if c.Constraint != (ids.ObjectAddress{}) && c.NamedIndex == (ids.ObjectAddress{}) {
		return sqlError("42809", "constraint in ON CONFLICT clause has no associated index")
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		def := index.SQLIndex
		if def.Parent != parent.Address() || !def.Live || !def.Ready || (!def.Unique && !def.IsExclusion()) {
			continue
		}
		if c.Constraint != (ids.ObjectAddress{}) {
			if index.Address() == c.NamedIndex {
				if c.Update != nil && def.IsExclusion() {
					return sqlError("42809", "ON CONFLICT DO UPDATE not supported with exclusion constraints")
				}
				c.selected = append(c.selected, index.Address())
			}
			continue
		}
		if len(c.Keys) == 0 {
			c.selected = append(c.selected, index.Address())
			continue
		}
		if !def.Unique {
			continue
		}
		if !def.Valid {
			continue
		}
		matches, err := c.matches(parent, def)
		if err != nil {
			return err
		}
		if !matches {
			continue
		}
		var predicate expr.Expr
		if def.Predicate != nil {
			predicate = def.Predicate.Definition
		}
		implied, err := conflictPredicateImpliedBy(ctx, c.Predicate, predicate)
		if err != nil {
			return err
		}
		if implied {
			c.selected = append(c.selected, index.Address())
		}
	}
	if (len(c.Keys) != 0 || c.Constraint != (ids.ObjectAddress{})) && len(c.selected) == 0 {
		return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
	}
	c.catalog = view.State()
	return nil
}

func (c *insertConflictState) matches(parent *catalog.Relation, index *catalog.IndexDefinition) (bool, error) {
	// Both directions compare sets, not key order/multiplicity. Decorations
	// restrict a matching key occurrence without changing the expression set.
	for _, target := range c.Keys {
		found := false
		for _, key := range index.Keys {
			attribute, expression, err := CatalogKey(parent, key)
			if err != nil {
				return false, err
			}
			if target.Attribute != attribute || !analyze.SameGroupExpression(target.Expression, expression) {
				continue
			}
			usage, err := KeyUsage(parent, key)
			if err != nil {
				return false, err
			}
			if target.Collation != 0 && target.Collation != usage.Collation.ID {
				continue
			}
			if target.Class.Name != "" && !target.Class.SameInferenceIdentity(key.Class) {
				continue
			}
			found = true
			break
		}
		if !found {
			return false, nil
		}
	}
	for _, key := range index.Keys {
		attribute, expression, err := CatalogKey(parent, key)
		if err != nil {
			return false, err
		}
		found := false
		for _, target := range c.Keys {
			if target.Attribute == attribute && analyze.SameGroupExpression(target.Expression, expression) {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
