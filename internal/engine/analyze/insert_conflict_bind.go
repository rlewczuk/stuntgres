package analyze

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Analysis resolves names/types/read manifests; selecting installed providers
// belongs to Execute, including commands with an empty source.
func bindInsertConflict(b *Binding, shared *Binder, auth Authority, raw *parser.InsertConflict) error {
	if raw == nil {
		return nil
	}
	if raw.Update && len(raw.Keys) == 0 && raw.Constraint.Text == "" {
		d := expr.ErrorAt("42601", "ON CONFLICT DO UPDATE requires inference specification or constraint name", raw.OnPos).(*sqlstate.Error)
		d.Hint = "For example, ON CONFLICT (column_name)."
		return d
	}
	c := &InsertConflict{Targets: b.Targets}
	b.Insert.Conflict = c
	if raw.Constraint.Text != "" {
		for _, u := range b.Relation.Constraints.Unique {
			if u.Name == raw.Constraint.Text {
				c.Constraint = ids.ObjectAddress{ClassID: 2606, ObjectID: u.OID}
				c.NamedIndex = u.Index
				for _, a := range u.Attributes {
					c.Reads = append(c.Reads, slices.Index(b.Relation.Attributes, a))
				}
				return nil
			}
		}
		for _, definition := range b.Relation.Constraints.Exclusion {
			if definition.Name != raw.Constraint.Text {
				continue
			}
			c.Constraint = ids.ObjectAddress{ClassID: 2606, ObjectID: definition.OID}
			c.NamedIndex = definition.Index
			index, ok := auth.ReadCatalog().Relation(definition.Index)
			if !ok {
				return fmt.Errorf("stuntgres: missing named exclusion index")
			}
			for _, key := range index.SQLIndex.Keys {
				if key.Derived == nil {
					c.Reads = append(c.Reads, slices.Index(b.Relation.Attributes, key.Attribute))
					continue
				}
				for _, dependency := range key.Derived.Dependencies {
					if dependency.Address.ClassID == b.Ref.Address.ClassID && dependency.Address.ObjectID == b.Ref.Address.ObjectID && dependency.Address.SubID != 0 {
						slot := slices.Index(b.Relation.Attributes, dependency.Address.SubID)
						if !slices.Contains(c.Reads, slot) {
							c.Reads = append(c.Reads, slot)
						}
					}
				}
			}
			return nil
		}
		for _, check := range b.Relation.Constraints.Check {
			if check.Name == raw.Constraint.Text {
				c.Constraint = ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}
				return nil
			}
		}
		for _, nn := range b.Relation.Constraints.NotNull {
			if nn.Name == raw.Constraint.Text {
				c.Constraint = ids.ObjectAddress{ClassID: 2606, ObjectID: nn.OID}
				return nil
			}
		}
		return sqlError("42704", fmt.Sprintf("constraint %q for table %q does not exist", raw.Constraint.Text, b.Relation.Name()))
	}
	binder := shared.Host.IndexExpressionBinder(shared.Input, auth, b.Relation)
	binder.Params.Parameters = shared.Params.Parameters
	binder.Expected = shared.Expected
	binder.Params.NoParameters = false
	if raw.Update {
		binder.Query.Hidden = append(binder.Query.Hidden, QueryQualifier{Name: "excluded", Original: "excluded", LateralForbidden: true})
	}
	c.Keys = make([]ConflictKey, len(raw.Keys))
	for i, key := range raw.Keys {
		if key.DirectionExplicit {
			return expr.ErrorAt("42P10", "ASC/DESC is not allowed in ON CONFLICT clause", raw.Pos)
		}
		if key.Nulls != parser.NullsDefault {
			return expr.ErrorAt("42P10", "NULLS FIRST/LAST is not allowed in ON CONFLICT clause", raw.Pos)
		}
		node := key.Expression
		if node == nil {
			node = parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: key.Column.Text, Pos: raw.Pos}}}, Pos: raw.Pos}
		}
		expression, err := binder.Bind(node, ExpressionPolicy{Context: ProjectionContext})
		if err != nil {
			return err
		}
		// Top-level COLLATE is a decoration, not index expression identity.
		for {
			collated, ok := expression.(plan.CollationExpr)
			if !ok {
				break
			}
			c.Keys[i].Collation = collated.ResultUsage().Collation.ID
			expression = collated.Child
		}
		if col, ok := expression.(plan.ColumnExpr); ok {
			c.Keys[i].Attribute = col.Origin.Address.SubID
		} else {
			c.Keys[i].Expression = expression
		}
		if key.Collation != nil {
			id, err := shared.Host.LookupCollation(*key.Collation)
			if err != nil {
				return expr.WithPosition(err, raw.Pos)
			}
			if expression.ResultUsage().Type.Usage().Collation.ID == 0 {
				return sqlError("42804", "collations are not supported by type "+expression.ResultUsage().Type.DiagnosticName())
			}
			c.Keys[i].Collation = id
		}
		if key.Opclass != nil {
			class, err := shared.Host.LookupIndexClass(auth, *key.Opclass, catalog.BtreeMethod)
			if err != nil {
				return err
			}
			c.Keys[i].Class = class
		}
		// Released inference parses class options but does not instantiate a CREATE
		// provider or apply its option validation; identity is family plus input.
	}
	if raw.Predicate != nil {
		binder.Consumer.IndexPredicateConsumer = true
		predicate, err := binder.BindBoolean(raw.Predicate, FilterContext, "WHERE")
		if err != nil {
			return err
		}
		c.Predicate = predicate
	}
	c.Reads = binder.Query.Sources[0].Uses
	shared.Params.AnyParameters = append(shared.Params.AnyParameters, binder.Params.AnyParameters...)
	for _, dep := range binder.Dependencies {
		shared.dependency(dep.Ref, dep.Revision)
	}
	return nil
}
