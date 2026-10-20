package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

type conflictKey struct {
	attribute  int32
	expression expr.Expr
	collation  uint32
	class      sqltype.IndexClass
}
type boundInsertConflict struct {
	keys          []conflictKey
	predicate     expr.Expr
	constraint    ids.ObjectAddress
	namedIndex    ids.ObjectAddress
	reads         []int
	selected      []ids.ObjectAddress
	catalog       *catalog.State
	checkpoint    lock.Checkpoint
	statementKeys lock.Checkpoint
	affected      int
	targets       []int
	update        *boundUpdate
	updateTargets []int
	actionReads   []int
	sources       []exec.Leaf
	returning     *boundReturning
	frame         []sqltype.Value
	width         int
	insertWriteOverlay
}

// instantiate owns one command's arbitration, preparation and write workspace.
// The analyzed expressions and manifests are immutable and may be shared.
func (c *boundInsertConflict) instantiate() *boundInsertConflict {
	if c == nil {
		return nil
	}
	return &boundInsertConflict{
		keys: c.keys, predicate: c.predicate, constraint: c.constraint,
		namedIndex: c.namedIndex, reads: c.reads, targets: c.targets,
		update: c.update, updateTargets: c.updateTargets,
		actionReads: c.actionReads, sources: c.sources,
		returning: c.returning, width: c.width,
	}
}

func (c *boundInsertConflict) selectArbiters(ctx *exec.Context, parent *catalog.Relation, view catalog.Read) error {
	c.selected = c.selected[:0]
	if c.constraint != (ids.ObjectAddress{}) && c.namedIndex == (ids.ObjectAddress{}) {
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
		if c.constraint != (ids.ObjectAddress{}) {
			if index.Address() == c.namedIndex {
				if c.update != nil && def.IsExclusion() {
					return sqlError("42809", "ON CONFLICT DO UPDATE not supported with exclusion constraints")
				}
				c.selected = append(c.selected, index.Address())
			}
			continue
		}
		if len(c.keys) == 0 {
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
		implied, err := conflictPredicateImpliedBy(ctx, c.predicate, predicate)
		if err != nil {
			return err
		}
		if implied {
			c.selected = append(c.selected, index.Address())
		}
	}
	if (len(c.keys) != 0 || c.constraint != (ids.ObjectAddress{})) && len(c.selected) == 0 {
		return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
	}
	c.catalog = view.State()
	return nil
}

func (c *boundInsertConflict) matches(parent *catalog.Relation, index *catalog.IndexDefinition) (bool, error) {
	// Both directions compare sets, not key order/multiplicity. Decorations
	// restrict a matching key occurrence without changing the expression set.
	for _, target := range c.keys {
		found := false
		for _, key := range index.Keys {
			attribute, expression, err := indexCatalogKey(parent, key)
			if err != nil {
				return false, err
			}
			if target.attribute != attribute || !sameGroupExpression(target.expression, expression) {
				continue
			}
			usage, err := indexKeyUsage(parent, key)
			if err != nil {
				return false, err
			}
			if target.collation != 0 && target.collation != usage.Collation.ID {
				continue
			}
			if target.class.Name != "" && !target.class.SameInferenceIdentity(key.Class) {
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
		attribute, expression, err := indexCatalogKey(parent, key)
		if err != nil {
			return false, err
		}
		found := false
		for _, target := range c.keys {
			if target.attribute == attribute && sameGroupExpression(target.expression, expression) {
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
