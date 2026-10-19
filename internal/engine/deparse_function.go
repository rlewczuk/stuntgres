package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) DeparseInquiry(signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if c.liveScope() == nil || c.liveScope().commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	if signature == functions.DeparseSerialSequence {
		return c.evalSerialSequence(values, result)
	}
	view := c.liveScope().commentCatalog
	pretty := false
	if prettyIndex := signature.PrettyIndex(); prettyIndex >= 0 {
		var err error
		pretty, err = values[prettyIndex].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	renderer := pgRenderer{view: view, pretty: pretty, path: c.liveScope().deparsePath,
		output: c.liveScope().output, nonstandardStrings: c.liveScope().op.nonstandardStrings}
	index := 1
	if signature != functions.DeparseExpr && signature != functions.DeparseExprPretty {
		index = 0
	}
	if signature >= functions.DeparseIndex {
		return c.deparseRelation(signature, values, result, pretty)
	}
	id, err := values[index].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var text string
	if signature == functions.DeparseConstraint || signature == functions.DeparseConstraintPretty {
		found := false
		for _, descriptor := range view.Descriptors() {
			if domain, ok := descriptor.(*catalog.Type); ok {
				for _, def := range domain.DomainChecks {
					if def.OID != ids.OID(id) {
						continue
					}
					found = true
					rendered, err := renderer.sql(c.Context, def.Expression, pgRenderParent{})
					if err != nil {
						return sqltype.Value{}, err
					}
					text = "CHECK (" + rendered + ")"
					if def.NotValid {
						text += " NOT VALID"
					}
				}
				continue
			}
			relation, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			for _, def := range relation.Constraints.NotNull {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				name, err := renderer.attribute(relation, def.Attribute)
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "NOT NULL " + name
				if def.NoInherit {
					text += " NO INHERIT"
				}
				if def.NotValid {
					text += " NOT VALID"
				}
			}
			for _, def := range relation.Constraints.Check {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				renderer.relation = relation
				rendered, err := renderer.sql(c.Context, def.Expression, pgRenderParent{})
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "CHECK (" + rendered + ")"
				if def.NoInherit {
					text += " NO INHERIT"
				}
				if def.NotValid {
					text += " NOT VALID"
				}
			}
			for _, def := range relation.Constraints.Unique {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.uniqueConstraintSQL(relation, &def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.ForeignKeys {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.foreignKeyConstraintSQL(relation, def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.Exclusion {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				index, ok := view.Relation(def.Index)
				if !ok {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing exclusion backing index")
				}
				text, err = indexDefinitionSQL(c.Context, view, index, indexPresentation{exclusion: true, pretty: pretty, path: c.liveScope().deparsePath, output: c.liveScope().output})
				if err != nil {
					return sqltype.Value{}, err
				}
			}
		}
		if !found {
			return sqltype.Null(result), nil
		}
	} else {
		carrier, err := values[0].Tree()
		if err != nil {
			return sqltype.Value{}, err
		}
		tree, ok := carrier.(boundTree)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign structural tree")
		}
		var relation *catalog.Relation
		if id != 0 {
			relation, ok = view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)})
			if !ok {
				return sqltype.Null(result), nil
			}
		}
		renderer.relation = relation
		text, err = renderer.sql(c.Context, tree.expression, pgRenderParent{})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(result, text), nil
}
func deparseAttribute(relation *catalog.Relation, attr int32) (string, error) {
	if relation == nil {
		return "", sqlError("22023", "expression contains variables")
	}
	for i, live := range relation.Attributes {
		if live == attr {
			return pgIdentifier(relation.Columns[i].Name), nil
		}
	}
	return "", sqlError("XX000", fmt.Sprintf("invalid attnum %d for relation %q", attr, relation.Name()))
}
func (c *EvalContext) deparseRelation(signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor, pretty bool) (sqltype.Value, error) {
	scope := c.liveScope()
	catalog := scope.commentCatalog
	var address ids.ObjectAddress
	if signature == functions.DeparseViewText || signature == functions.DeparseViewTextPretty {
		text, err := values[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		name, err := parser.ParseRegclassName(text)
		if err != nil || len(name.Parts) > 2 {
			return sqltype.Value{}, sqlError("42602", "invalid name syntax")
		}
		for i := range name.Parts {
			if name.Parts[i].Text == "" {
				return sqltype.Value{}, sqlError("42602", "invalid name syntax")
			}
			name.Parts[i].Pos = parser.Position{}
		}
		ref, err := scope.deparseAuth.engine.resolveRelationSnapshot(scope.deparseAuth, name, true)
		if err != nil {
			return sqltype.Value{}, err
		}
		address = ref.Address
	} else {
		id, err := values[0].OID()
		if err != nil {
			return sqltype.Value{}, err
		}
		address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)}
	}
	relation, ok := catalog.Relation(address)
	if !ok {
		return sqltype.Null(result), nil
	}
	var text string
	var err error
	if signature == functions.DeparseIndex || signature == functions.DeparseIndexColumn {
		if relation.SQLIndex == nil {
			return sqltype.Null(result), nil
		}
		var column int64
		if signature == functions.DeparseIndexColumn {
			column, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		text, err = indexDefinitionSQL(c.Context, catalog, relation, indexPresentation{column: column, pretty: pretty, path: scope.deparsePath, temporary: temporaryNamespace(scope.deparseAuth), output: scope.output})
	} else {
		if relation.View == nil {
			return sqltype.Null(result), nil
		}
		var wrap int64
		if signature == functions.DeparseViewWrap {
			pretty = true
			wrap, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		text, err = viewOf(relation).selectSQL(c.Context, catalog, viewPresentation{deparse: true, pretty: pretty, wrap: wrap, path: scope.deparsePath, columns: relation.Columns, output: scope.output, nonstandardStrings: scope.op.nonstandardStrings})
		text += ";"
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text), nil
}
