package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) DeparseInquiry(c *exec.Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	if signature == functions.DeparseSerialSequence {
		return evalSerialSequence(c, values, result)
	}
	view := scope.inquiry.commentCatalog
	pretty := false
	if prettyIndex := signature.PrettyIndex(); prettyIndex >= 0 {
		var err error
		pretty, err = values[prettyIndex].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	renderer := stored.Deparser{View: view, Pretty: pretty, Path: scope.inquiry.deparsePath,
		Output: scope.output, NonstandardStrings: scope.op.NonstandardStrings}
	idx := 1
	if signature != functions.DeparseExpr && signature != functions.DeparseExprPretty {
		idx = 0
	}
	if signature >= functions.DeparseIndex {
		return deparseRelation(c, signature, values, result, pretty)
	}
	id, err := values[idx].OID()
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
					rendered, err := renderer.SQL(c.Context, def.Expression, stored.DeparseParent{})
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
				name, err := renderer.Attribute(relation, def.Attribute)
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
				renderer.Relation = relation
				rendered, err := renderer.SQL(c.Context, def.Expression, stored.DeparseParent{})
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
				text, err = renderer.UniqueConstraintSQL(relation, &def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.ForeignKeys {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.ForeignKeyConstraintSQL(relation, def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.Exclusion {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				idx, ok := view.Relation(def.Index)
				if !ok {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing exclusion backing index")
				}
				text, err = index.DefinitionSQL(c.Context, view, idx, index.Presentation{Exclusion: true, Pretty: pretty, Path: scope.inquiry.deparsePath, Output: scope.output})
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
		tree, ok := carrier.(plan.BoundTree)
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
		renderer.Relation = relation
		text, err = renderer.SQL(c.Context, tree.Expression, stored.DeparseParent{})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(result, text), nil
}
func deparseRelation(c *exec.Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor, pretty bool) (sqltype.Value, error) {
	scope := scopeOf(c.LiveHost())
	catalog := scope.inquiry.commentCatalog
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
		ref, err := resolveRelationSnapshot(scope.inquiry.deparseAuth, name, true)
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
		text, err = index.DefinitionSQL(c.Context, catalog, relation, index.Presentation{Column: column, Pretty: pretty, Path: scope.inquiry.deparsePath, Temporary: temporaryNamespace(scope.inquiry.deparseAuth), Output: scope.output})
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
		text, err = stored.ViewOf(relation).SelectSQL(c.Context, catalog, stored.Presentation{Deparse: true, Pretty: pretty, Wrap: wrap, Path: scope.inquiry.deparsePath, Columns: relation.Columns, Output: scope.output, NonstandardStrings: scope.op.NonstandardStrings})
		text += ";"
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text), nil
}
