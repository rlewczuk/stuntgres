package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type conflictKey struct {
	attribute  int32
	expression BoundExpr
	collation  uint32
	class      sqltype.IndexClass
}
type boundInsertConflict struct {
	keys          []conflictKey
	predicate     BoundExpr
	constraint    string
	namedIndex    ObjectAddress
	reads         []int
	selected      []ObjectAddress
	catalog       *catalogState
	checkpoint    relationLockCheckpoint
	statementKeys relationLockCheckpoint
	affected      int
	targets       []int
	update        *boundUpdate
	updateTargets []int
	actionReads   []int
	sources       []queryLeaf
	frame         []sqltype.Value
	width         int
	affectedRows  map[RowID]int
	ownRowSlots   map[RowID]int
	overlays      map[*uniqueBoundKey]*conflictUpdateOverlay
}

// Analysis resolves names/types/read manifests; selecting installed providers
// belongs to Execute, including commands with an empty source.
func (e *Engine) bindInsertConflict(b *binding, shared *expressionBinder, auth AuthorizationView, raw *parser.InsertConflict) error {
	if raw == nil {
		return nil
	}
	if raw.Update && len(raw.Keys) == 0 && raw.Constraint.Text == "" {
		d := expressionError("42601", "ON CONFLICT DO UPDATE requires inference specification or constraint name", raw.OnPos).(*sqlstate.Error)
		d.Hint = "For example, ON CONFLICT (column_name)."
		return d
	}
	c := &boundInsertConflict{constraint: raw.Constraint.Text, targets: b.targets}
	b.conflict = c
	if c.constraint != "" {
		for _, u := range b.relation.uniqueConstraints {
			if u.Name == c.constraint {
				c.namedIndex = u.Index
				for _, a := range u.Attributes {
					c.reads = append(c.reads, slices.Index(b.relation.attributes, a))
				}
				return nil
			}
		}
		for _, definition := range b.relation.exclusionConstraints {
			if definition.Name != c.constraint {
				continue
			}
			c.namedIndex = definition.Index
			index, ok := auth.catalog.state.descriptors[definition.Index].(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: missing named exclusion index")
			}
			for _, key := range index.sqlIndex.keys {
				if key.derived == nil {
					c.reads = append(c.reads, slices.Index(b.relation.attributes, key.attribute))
					continue
				}
				for _, dependency := range key.derived.dependencies {
					if dependency.Address.ClassID == b.ref.Address.ClassID && dependency.Address.ObjectID == b.ref.Address.ObjectID && dependency.Address.SubID != 0 {
						slot := slices.Index(b.relation.attributes, dependency.Address.SubID)
						if !slices.Contains(c.reads, slot) {
							c.reads = append(c.reads, slot)
						}
					}
				}
			}
			return nil
		}
		for _, check := range b.relation.checkConstraints {
			if check.Name == c.constraint {
				return nil
			}
		}
		for _, nn := range b.relation.notNullConstraints {
			if nn.Name == c.constraint {
				return nil
			}
		}
		return sqlError("42704", fmt.Sprintf("constraint %q for table %q does not exist", c.constraint, b.relation.name))
	}
	binder := e.indexExpressionBinder(shared.op, shared.input, auth, b.relation)
	binder.parameters = shared.parameters
	binder.expected = shared.expected
	binder.noParameters = false
	if raw.Update {
		binder.queryHidden = append(binder.queryHidden, queryQualifier{name: "excluded", original: "excluded", lateralForbidden: true})
	}
	c.keys = make([]conflictKey, len(raw.Keys))
	for i, key := range raw.Keys {
		if key.DirectionExplicit {
			return expressionError("42P10", "ASC/DESC is not allowed in ON CONFLICT clause", raw.Pos)
		}
		if key.Nulls != parser.NullsDefault {
			return expressionError("42P10", "NULLS FIRST/LAST is not allowed in ON CONFLICT clause", raw.Pos)
		}
		node := key.Expression
		if node == nil {
			node = parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: key.Column.Text, Pos: raw.Pos}}}, Pos: raw.Pos}
		}
		expression, err := binder.bind(node, expressionPolicy{context: projectionContext})
		if err != nil {
			return err
		}
		// Top-level COLLATE is a decoration, not index expression identity.
		for {
			collated, ok := expression.(collationExpression)
			if !ok {
				break
			}
			c.keys[i].collation = collated.ResultUsage().Collation.ID
			expression = collated.child
		}
		if col, ok := expression.(columnExpression); ok {
			c.keys[i].attribute = col.origin.Address.SubID
		} else {
			c.keys[i].expression = expression
		}
		if key.Collation != nil {
			id, err := e.lookupCollation(*key.Collation, shared.op)
			if err != nil {
				return expressionErrorFrom(err, raw.Pos)
			}
			if expression.ResultUsage().Type.Usage().Collation.ID == 0 {
				return sqlError("42804", "collations are not supported by type "+expression.ResultUsage().Type.DiagnosticName())
			}
			c.keys[i].collation = id
		}
		if key.Opclass != nil {
			class, err := e.lookupIndexClass(auth, *key.Opclass, btreeMethod)
			if err != nil {
				return err
			}
			c.keys[i].class = class
		}
		// Released inference parses class options but does not instantiate a CREATE
		// provider or apply its option validation; identity is family plus input.
	}
	if raw.Predicate != nil {
		binder.indexPredicateConsumer = true
		predicate, err := binder.bindBoolean(raw.Predicate, filterContext, "WHERE")
		if err != nil {
			return err
		}
		c.predicate = predicate
	}
	c.reads = binder.querySources[0].uses
	shared.anyParameters = append(shared.anyParameters, binder.anyParameters...)
	for _, dep := range binder.dependencies {
		shared.dependency(dep.ref, dep.revision)
	}
	return nil
}

func expressionErrorFrom(err error, pos parser.Position) error {
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Position = strconv.Itoa(pos.Character)
		return &copy
	}
	return err
}

func (c *boundInsertConflict) selectArbiters(ctx *EvalContext, parent *relationDescriptor, view CatalogRead) error {
	c.selected = c.selected[:0]
	if c.constraint != "" && c.namedIndex == (ObjectAddress{}) {
		return sqlError("42809", "constraint in ON CONFLICT clause has no associated index")
	}
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		def := index.sqlIndex
		if def.parent != parent.address || !def.live || !def.ready || (!def.unique && !def.isExclusion()) {
			continue
		}
		if c.constraint != "" {
			if index.address == c.namedIndex {
				if c.update != nil && def.isExclusion() {
					return sqlError("42809", "ON CONFLICT DO UPDATE not supported with exclusion constraints")
				}
				c.selected = append(c.selected, index.address)
			}
			continue
		}
		if len(c.keys) == 0 {
			c.selected = append(c.selected, index.address)
			continue
		}
		if !def.unique {
			continue
		}
		if !def.valid {
			continue
		}
		matches, err := c.matches(parent, def)
		if err != nil {
			return err
		}
		if !matches {
			continue
		}
		var predicate BoundExpr
		if def.predicate != nil {
			predicate = def.predicate.definition
		}
		implied, err := conflictPredicateImpliedBy(ctx, c.predicate, predicate)
		if err != nil {
			return err
		}
		if implied {
			c.selected = append(c.selected, index.address)
		}
	}
	if (len(c.keys) != 0 || c.constraint != "") && len(c.selected) == 0 {
		return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
	}
	c.catalog = view.state
	return nil
}

func (c *boundInsertConflict) matches(parent *relationDescriptor, index *indexDefinition) (bool, error) {
	// Both directions compare sets, not key order/multiplicity. Decorations
	// restrict a matching key occurrence without changing the expression set.
	for _, target := range c.keys {
		found := false
		for _, key := range index.keys {
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
			if target.class.Name != "" && !target.class.SameInferenceIdentity(key.class) {
				continue
			}
			found = true
			break
		}
		if !found {
			return false, nil
		}
	}
	for _, key := range index.keys {
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
