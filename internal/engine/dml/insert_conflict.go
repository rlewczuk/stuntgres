package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// InsertConflictState is one command's ON CONFLICT arbitration: the bound
// clause it executes, the selected arbiters, unique-key checkpoints, the
// affected count, the EXCLUDED frame and the write overlay.
type InsertConflictState struct {
	*analyze.InsertConflict
	Selected      []ids.ObjectAddress
	Catalog       *catalog.State
	Checkpoint    lock.Checkpoint
	StatementKeys lock.Checkpoint
	Affected      int
	Frame         []sqltype.Value
	InsertWriteOverlay
}

// NewInsertConflictState starts the arbitration state of one command over c,
// or returns nil without a clause.
func NewInsertConflictState(c *analyze.InsertConflict) *InsertConflictState {
	if c == nil {
		return nil
	}
	return &InsertConflictState{InsertConflict: c}
}

func (c *InsertConflictState) SelectArbiters(ctx *exec.Context, parent *catalog.Relation, view catalog.Read) error {
	c.Selected = c.Selected[:0]
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
				c.Selected = append(c.Selected, index.Address())
			}
			continue
		}
		if len(c.Keys) == 0 {
			c.Selected = append(c.Selected, index.Address())
			continue
		}
		if !def.Unique {
			continue
		}
		if !def.Valid {
			continue
		}
		matches, err := c.Matches(parent, def)
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
		implied, err := ConflictPredicateImpliedBy(ctx, c.Predicate, predicate)
		if err != nil {
			return err
		}
		if implied {
			c.Selected = append(c.Selected, index.Address())
		}
	}
	if (len(c.Keys) != 0 || c.Constraint != (ids.ObjectAddress{})) && len(c.Selected) == 0 {
		return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
	}
	c.Catalog = view.State()
	return nil
}

func (c *InsertConflictState) Matches(parent *catalog.Relation, idx *catalog.IndexDefinition) (bool, error) {
	// Both directions compare sets, not key order/multiplicity. Decorations
	// restrict a matching key occurrence without changing the expression set.
	for _, target := range c.Keys {
		found := false
		for _, key := range idx.Keys {
			attribute, expression, err := index.CatalogKey(parent, key)
			if err != nil {
				return false, err
			}
			if target.Attribute != attribute || !analyze.SameGroupExpression(target.Expression, expression) {
				continue
			}
			usage, err := index.KeyUsage(parent, key)
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
	for _, key := range idx.Keys {
		attribute, expression, err := index.CatalogKey(parent, key)
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

func BindingForConflict(ref ids.ObjectRef, parent *catalog.Relation, action *InsertConflictState) analyze.Binding {
	return analyze.Binding{Ref: ref, Relation: parent, Targets: action.Targets, Insert: analyze.InsertBinding{Conflict: action.InsertConflict}, QuerySources: action.Sources, Returning: action.Returning}
}

func (c *InsertConflictState) MarkAffected(id ids.RowID, slot int) {
	if c.AffectedRows == nil {
		c.AffectedRows = make(map[ids.RowID]int)
	}
	c.AffectedRows[id] = slot
	c.Affected++
}

func ConflictUpdateCardinalityError() error {
	d := sqlError("21000", "ON CONFLICT DO UPDATE command cannot affect row a second time").(*sqlstate.Error)
	d.Hint = "Ensure that no rows proposed for insertion within the same command have duplicate constrained values."
	return d
}
